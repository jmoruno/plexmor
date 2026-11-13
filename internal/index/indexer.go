package index

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jmoruno/plexmor/internal/plex"
)

// Source is a server to index: its identity and how to reach it.
type Source struct {
	MachineID string
	Name      string
	Owner     string
	Owned     bool
	API       plex.Server
}

// Indexer reads the libraries of a server and publishes them into the store.
type Indexer struct {
	Client      *plex.Client
	Store       *Store
	Log         *slog.Logger
	Timeout     time.Duration // per Plex request
	PageSize    int           // items per listing page
	BatchSize   int           // items per /library/metadata request
	Concurrency int           // parallel metadata requests per server
	// MaxConsecutiveFailures aborts the sync when this many detail batches fail in a row:
	// the server has most likely gone away and the previous index is kept.
	MaxConsecutiveFailures int
	// RetryDelay is the base wait between attempts of a failed request.
	RetryDelay time.Duration
}

// Stats summarizes one sync.
type Stats struct {
	Items          int
	Detailed       int // items whose tracks were (re)read
	Reused         int // unchanged items kept from the previous sync
	Removed        int
	FailedSections int // libraries that could not be read (their previous items are kept)
	Pending        int // items still without track details (retried on the next sync)
}

// ErrServerUnresponsive means the server stopped answering in the middle of a sync.
var ErrServerUnresponsive = errors.New("server stopped answering during the sync")

// Sync indexes one server. If the server cannot be listed or stops answering, the previous
// data of the server is kept untouched. A library that fails on its own keeps its previous items.
func (x *Indexer) Sync(ctx context.Context, src Source) (Stats, error) {
	prev := x.Store.Server(src.MachineID)
	next := NewServerData(src.MachineID)
	next.Name, next.Owner, next.Owned = src.Name, src.Owner, src.Owned

	var sections []plex.Section
	err := x.retry(ctx, func(c context.Context) (err error) {
		sections, err = x.Client.Sections(c, src.API)
		return err
	})
	if err != nil {
		return Stats{}, err
	}

	var st Stats
	var pending []*Item // items whose tracks must be read
	for _, sec := range sections {
		if sec.Type != "movie" && sec.Type != "show" {
			continue
		}
		next.Sections = append(next.Sections, Section{Key: sec.Key, Title: sec.Title, Type: sec.Type})
		items, shows, err := x.section(ctx, src, sec)
		if err != nil {
			if ctx.Err() != nil || plex.Unauthorized(err) {
				return Stats{}, err
			}
			st.FailedSections++
			x.Log.Warn("library could not be read, keeping its previous items", "server", src.Name, "library", sec.Title, "error", err)
			carryOver(prev, next, sec.Key)
			continue
		}
		for _, s := range shows {
			next.Shows[s.RatingKey] = s
		}
		for _, it := range items {
			if p := prevItem(prev, it.RatingKey); p != nil && p.UpdatedAt == it.UpdatedAt && detailed(p) && sameParts(p, it) {
				next.Items[it.RatingKey] = p
				st.Reused++
				continue
			}
			next.Items[it.RatingKey] = it
			pending = append(pending, it)
		}
	}

	detailedItems, err := x.details(ctx, src, pending)
	if err != nil {
		return Stats{}, err
	}
	for _, it := range pending {
		if d, ok := detailedItems[it.RatingKey]; ok {
			next.Items[it.RatingKey] = d
			st.Detailed++
			continue
		}
		// No details this time: keep the tracks we already knew rather than losing the languages.
		if p := prevItem(prev, it.RatingKey); p != nil && detailed(p) && sameParts(p, it) {
			next.Items[it.RatingKey] = p
		}
		st.Pending++
	}
	st.Items = len(next.Items)
	if prev != nil {
		for k := range prev.Items {
			if _, ok := next.Items[k]; !ok {
				st.Removed++
			}
		}
	}
	next.SyncedAt = time.Now().Unix()
	x.Store.PutServer(next)
	return st, nil
}

// section lists one library: movies, or series with their episodes.
func (x *Indexer) section(ctx context.Context, src Source, sec plex.Section) ([]*Item, []*Show, error) {
	var items []*Item
	var shows []*Show
	if sec.Type == "movie" {
		err := x.list(ctx, src.API, sec.Key, plex.TypeMovie, func(m plex.Metadata) {
			items = append(items, fromMetadata(src.MachineID, sec.Key, m))
		})
		return items, nil, err
	}
	err := x.list(ctx, src.API, sec.Key, plex.TypeShow, func(m plex.Metadata) {
		shows = append(shows, &Show{
			RatingKey: m.RatingKey, Section: sec.Key, Title: m.Title, IDs: ParseIDs(m.Guids, m.LegacyGUID),
			Year: m.Year, Thumb: m.Thumb, Art: m.Art, Summary: m.Summary, Genres: tags(m.Genres), AddedAt: m.AddedAt,
		})
	})
	if err != nil {
		return nil, nil, err
	}
	err = x.list(ctx, src.API, sec.Key, plex.TypeEpisode, func(m plex.Metadata) {
		items = append(items, fromMetadata(src.MachineID, sec.Key, m))
	})
	return items, shows, err
}

// carryOver copies into next the previous items and series of a library that could not be read.
func carryOver(prev, next *ServerData, section string) {
	if prev == nil {
		return
	}
	for k, it := range prev.Items {
		if it.Section == section {
			next.Items[k] = it
		}
	}
	for k, s := range prev.Shows {
		if s.Section == section {
			next.Shows[k] = s
		}
	}
}

// list walks a whole listing page by page; a failed page is retried on its own.
func (x *Indexer) list(ctx context.Context, srv plex.Server, section string, itemType int, fn func(plex.Metadata)) error {
	for start := 0; ; {
		var page []plex.Metadata
		var total int
		err := x.retry(ctx, func(c context.Context) (err error) {
			page, total, err = x.Client.Page(c, srv, section, itemType, start, x.PageSize)
			return err
		})
		if err != nil {
			return err
		}
		for _, m := range page {
			fn(m)
		}
		start += len(page)
		if len(page) < x.PageSize || (total > 0 && start >= total) {
			return nil
		}
	}
}

// details reads the tracks of the pending items in batches, a few batches at a time.
// An isolated failed batch is retried on the next sync; several failures in a row (or an
// authorization error) mean the server is gone, and the whole sync is aborted.
func (x *Indexer) details(ctx context.Context, src Source, pending []*Item) (map[string]*Item, error) {
	byKey := make(map[string]*Item, len(pending))
	for _, it := range pending {
		byKey[it.RatingKey] = it
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var (
		mu          sync.Mutex
		wg          sync.WaitGroup
		out         = make(map[string]*Item, len(pending))
		failures    int
		consecutive int
	)
	limit := max(1, x.MaxConsecutiveFailures)
	sem := make(chan struct{}, max(1, x.Concurrency))
loop:
	for start := 0; start < len(pending); start += x.BatchSize {
		end := min(start+x.BatchSize, len(pending))
		keys := make([]string, 0, end-start)
		for _, it := range pending[start:end] {
			keys = append(keys, it.RatingKey)
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		wg.Add(1)
		go func(keys []string) {
			defer wg.Done()
			defer func() { <-sem }()
			var metas []plex.Metadata
			err := x.retry(ctx, func(c context.Context) (err error) {
				metas, err = x.Client.Metadata(c, src.API, keys)
				return err
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				failures++
				consecutive++
				x.Log.Debug("metadata batch failed", "server", src.Name, "items", len(keys), "error", err)
				if plex.Unauthorized(err) || consecutive >= limit {
					cancel(fmt.Errorf("%w: %v", ErrServerUnresponsive, err))
				}
				return
			}
			consecutive = 0
			for _, m := range metas {
				if base, ok := byKey[m.RatingKey]; ok {
					out[m.RatingKey] = withTracks(base, m)
				}
			}
		}(keys)
	}
	wg.Wait()
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	if failures > 0 {
		x.Log.Warn("some items have no track details yet", "server", src.Name, "failedBatches", failures)
	}
	return out, nil
}

// retry runs fn up to three times with a per-attempt timeout. It does not retry
// authorization errors and does not wait after the last attempt.
func (x *Indexer) retry(ctx context.Context, fn func(context.Context) error) error {
	delay := x.RetryDelay
	if delay <= 0 {
		delay = time.Second
	}
	const attempts = 3
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		c, cancel := context.WithTimeout(ctx, x.Timeout)
		err = fn(c)
		cancel()
		if err == nil || plex.Unauthorized(err) || ctx.Err() != nil {
			if ctx.Err() != nil && err != nil {
				return context.Cause(ctx)
			}
			return err
		}
		if attempt == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-time.After(delay * time.Duration(attempt)):
		}
	}
	return err
}

func prevItem(prev *ServerData, key string) *Item {
	if prev == nil {
		return nil
	}
	return prev.Items[key]
}

func detailed(it *Item) bool {
	for _, v := range it.Versions {
		if !v.Detailed {
			return false
		}
	}
	return true
}

func sameParts(a, b *Item) bool {
	if len(a.Versions) != len(b.Versions) {
		return false
	}
	for i := range a.Versions {
		if a.Versions[i].PartKey != b.Versions[i].PartKey {
			return false
		}
	}
	return true
}

// fromMetadata builds an item from a listing entry (versions without tracks).
func fromMetadata(server, section string, m plex.Metadata) *Item {
	it := &Item{
		Server:    server,
		Section:   section,
		RatingKey: m.RatingKey,
		Type:      m.Type,
		Title:     m.Title,
		Year:      m.Year,
		UpdatedAt: m.UpdatedAt,
		IDs:       ParseIDs(m.Guids, m.LegacyGUID),
		Thumb:     m.Thumb,
		Art:       m.Art,
		Summary:   m.Summary,
		Genres:    tags(m.Genres),
		AddedAt:   m.AddedAt,
		Released:  m.OriginallyAvailableAt,
		Duration:  m.Duration,
	}
	if m.Type == "episode" {
		it.ShowKey, it.Show, it.Season, it.Episode = m.GrandparentRatingKey, m.GrandparentTitle, m.ParentIndex, m.Index
	}
	it.Versions = versions(m.Media, false)
	return it
}

// withTracks returns a copy of base with the versions rebuilt from the detailed metadata.
func withTracks(base *Item, m plex.Metadata) *Item {
	cp := *base
	if ids := ParseIDs(m.Guids, m.LegacyGUID); ids != (IDs{}) {
		cp.IDs = ids
	}
	cp.Versions = versions(m.Media, true)
	if len(cp.Versions) == 0 {
		cp.Versions = base.Versions
	}
	return &cp
}

func versions(media []plex.Media, withTracks bool) []Version {
	var out []Version
	for mi, md := range media {
		for i, part := range md.Parts {
			v := version(md, part)
			v.MediaIndex = mi
			if len(md.Parts) > 1 {
				v.Part, v.Parts = i+1, len(md.Parts)
			}
			if withTracks {
				v.Detailed = true
				applyTracks(&v, part.Streams)
			}
			out = append(out, v)
		}
	}
	return out
}

func version(media plex.Media, part plex.Part) Version {
	container := part.Container
	if container == "" {
		container = media.Container
	}
	return Version{
		PartKey:    part.Key,
		File:       path.Base(strings.ReplaceAll(part.File, "\\", "/")),
		Container:  container,
		Size:       part.Size,
		Bitrate:    media.Bitrate,
		Duration:   media.Duration,
		Resolution: strings.ToLower(media.VideoResolution),
		Width:      media.Width,
		Height:     media.Height,
		VideoCodec: strings.ToLower(media.VideoCodec),
	}
}

func applyTracks(v *Version, streams []plex.Stream) {
	seenSub := map[string]bool{}
	for _, s := range streams {
		switch s.StreamType {
		case plex.StreamVideo:
			switch {
			case s.DOVIPresent:
				v.HDR = "DV"
			case strings.EqualFold(s.ColorTrc, "smpte2084"):
				v.HDR = "HDR10"
			case strings.EqualFold(s.ColorTrc, "arib-std-b67"):
				v.HDR = "HLG"
			}
		case plex.StreamAudio:
			v.Audio = append(v.Audio, Audio{Language: Language(s), Codec: strings.ToLower(s.Codec), Channels: s.Channels})
		case plex.StreamSubtitle:
			sub := Subtitle{Language: Language(s), Forced: s.Forced, Codec: strings.ToLower(s.Codec), Key: s.Key}
			key := sub.Language
			if sub.Forced {
				key += ":forced"
			}
			if sub.Key != "" {
				key += ":" + sub.Key // external files are kept apart: Stremio can load them
			}
			if !seenSub[key] {
				seenSub[key] = true
				v.Subtitles = append(v.Subtitles, sub)
			}
		}
	}
}

func tags(ts []plex.Tag) []string {
	if len(ts) == 0 {
		return nil
	}
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Tag)
	}
	return out
}
