package stremio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/plex"
	"github.com/jmoruno/plexmor/internal/plextest"
)

// homeVideo has no IMDb id: it only exists in Plex.
var homeVideo = plex.Metadata{
	RatingKey: "50", Type: "movie", Title: "Boda de Ana", Year: 2019, UpdatedAt: 1, AddedAt: 2000,
	Thumb: "/library/metadata/50/thumb/1", Art: "/library/metadata/50/art/1", Summary: "Vídeo de la boda",
	Genres: []plex.Tag{{Tag: "Familia"}},
	Media: []plex.Media{{ID: 50, VideoResolution: "1080", Height: 1080, VideoCodec: "h264", Container: "mp4",
		Parts: []plex.Part{{ID: 50, Key: "/library/parts/50/1/boda.mp4", File: "boda.mp4", Size: 1 << 30,
			Streams: []plex.Stream{
				audio("es", "", 2),
				{StreamType: plex.StreamSubtitle, LanguageTag: "en", Codec: "srt", Key: "/library/streams/777"},
				{StreamType: plex.StreamSubtitle, LanguageTag: "es", Codec: "pgs"}, // embedded image subtitles: not offered
			}}}}},
}

func featureFixture(t *testing.T, mode string, prefs func(*accounts.Prefs)) *fixture {
	t.Helper()
	f := newFixture(t)
	f.own.Sections[0].Items = append(f.own.Sections[0].Items, homeVideo)
	f.own.Details["50"] = homeVideo
	if _, err := f.accs.Update(testAcc, func(a *accounts.Account) {
		a.Prefs.StreamMode = mode
		if prefs != nil {
			prefs(&a.Prefs)
		}
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := f.reg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	f.reg.Probe(ctx)
	f.syncAll(t)
	return f
}

func fetch(t *testing.T, h http.Handler, path string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "plexmor.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	return v
}

func TestProxyHidesTokenAndServesRanges(t *testing.T) {
	f := featureFixture(t, accounts.ModeAuto, nil)
	body := decode[struct{ Streams []Stream }](t, fetch(t, f.handler, accPath("/stream/movie/tt0816692.json"), nil))
	var proxied *Stream
	for i, s := range body.Streams {
		if strings.Contains(s.Description, "Casa") {
			proxied = &body.Streams[i]
		}
		if strings.Contains(s.URL, "Token") {
			t.Fatalf("auto mode: the home server (127.0.0.1) must go through Plexmor, got %s", s.URL)
		}
	}
	want := "https://plexmor.example.com/" + testAcc + "/play/own-id/library/parts/10/1/file.mkv"
	if proxied == nil || proxied.URL != want || !strings.Contains(proxied.Description, "→ Plexmor") {
		t.Fatalf("proxied stream: %+v", proxied)
	}
	rec := fetch(t, f.handler, strings.TrimPrefix(want, "https://plexmor.example.com"), map[string]string{"Range": "bytes=0-6"})
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "CONTENT" || rec.Header().Get("Content-Range") == "" {
		t.Fatalf("range request through the proxy: %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	for _, bad := range []string{"/play/own-id/library/sections", "/play/own-id/library/parts/../../identity", "/play/otro/library/parts/1/2/x.mkv"} {
		if rec := fetch(t, f.handler, accPath(bad), nil); rec.Code == http.StatusOK {
			t.Fatalf("%s must be refused, got %d", bad, rec.Code)
		}
	}
}

func TestProxyModeAlwaysAndDirect(t *testing.T) {
	f := featureFixture(t, accounts.ModeProxy, nil)
	for _, s := range decode[struct{ Streams []Stream }](t, fetch(t, f.handler, accPath("/stream/movie/tt0816692.json"), nil)).Streams {
		if !strings.HasPrefix(s.URL, "https://plexmor.example.com/"+testAcc+"/play/") {
			t.Fatalf("proxy mode must proxy every server: %s", s.URL)
		}
	}
	f = featureFixture(t, accounts.ModeDirect, nil)
	for _, s := range decode[struct{ Streams []Stream }](t, fetch(t, f.handler, accPath("/stream/movie/tt0816692.json"), nil)).Streams {
		if !strings.Contains(s.URL, "X-Plex-Token=") {
			t.Fatalf("direct mode goes straight to Plex: %s", s.URL)
		}
	}
}

func TestTranscodedStreamsAndPlaylistRewrite(t *testing.T) {
	f := featureFixture(t, accounts.ModeProxy, func(p *accounts.Prefs) { p.Transcode = true })
	streams := decode[struct{ Streams []Stream }](t, fetch(t, f.handler, accPath("/stream/movie/tt0816692.json"), nil)).Streams
	var t720 *Stream
	for i, s := range streams {
		if strings.Contains(s.Name, "720p (transcodificado)") && strings.Contains(s.Description, "desde 4K") {
			t720 = &streams[i]
		}
	}
	if t720 == nil || !strings.Contains(t720.URL, "/video/:/transcode/universal/start.m3u8?") || t720.BehaviorHints.NotWebReady {
		t.Fatalf("a 720p transcoded version of the 4K should be offered: %+v", streams)
	}
	if !strings.Contains(streams[0].Name, "4K") || strings.Contains(streams[0].Name, "transcod") {
		t.Fatalf("originals go first: %s", streams[0].Name)
	}
	rec := fetch(t, f.handler, strings.TrimPrefix(t720.URL, "https://plexmor.example.com"), nil)
	body := rec.Body.String()
	if !strings.Contains(body, "\nsession/abc/base/index.m3u8\n") || !strings.Contains(body, "\n/"+testAcc+"/play/friend-id/video/:/transcode/universal/session/abc/1/index.m3u8\n") {
		t.Fatalf("playlist paths must stay inside the proxy:\n%s", body)
	}
}

func TestResolutionFilterAndLimit(t *testing.T) {
	f := featureFixture(t, accounts.ModeDirect, func(p *accounts.Prefs) { p.Resolutions = []string{"1080", "720"} })
	streams := decode[struct{ Streams []Stream }](t, fetch(t, f.handler, accPath("/stream/movie/tt0816692.json"), nil)).Streams
	if len(streams) != 2 {
		t.Fatalf("the 4K version must be hidden, got %d streams", len(streams))
	}
}

func TestExcludedLibraryIsHidden(t *testing.T) {
	f := featureFixture(t, accounts.ModeDirect, func(p *accounts.Prefs) { p.ExcludedServers = []string{"friend-id"} })
	streams := decode[struct{ Streams []Stream }](t, fetch(t, f.handler, accPath("/stream/movie/tt0816692.json"), nil)).Streams
	if len(streams) != 1 || !strings.Contains(streams[0].Description, "Casa") {
		t.Fatalf("only the own server should remain: %d", len(streams))
	}
}

func TestCatalogsSearchAndMeta(t *testing.T) {
	f := featureFixture(t, accounts.ModeAuto, nil)
	m := decode[manifest](t, fetch(t, f.handler, accPath("/manifest.json"), nil))
	ids := map[string]bool{}
	for _, c := range m.Catalogs {
		ids[c.Type+"/"+c.ID] = true
	}
	for _, want := range []string{"movie/recent", "series/recent", "movie/search", "movie/ondeck", "movie/watchlist", "movie/lib.own-id.1", "series/lib.own-id.2", "movie/lib.friend-id.5"} {
		if !ids[want] {
			t.Fatalf("catalog %s missing: %v", want, ids)
		}
	}
	if ids["movie/trakt.watchlist"] {
		t.Fatal("Trakt catalogs need the module")
	}

	recent := decode[struct{ Metas []MetaPreview }](t, fetch(t, f.handler, accPath("/catalog/movie/recent.json"), nil)).Metas
	if len(recent) != 2 || recent[0].ID != "plexmor:own-id:50" || recent[1].ID != "tt0816692" {
		t.Fatalf("recent movies (newest first, one entry per title): %+v", recent)
	}
	if recent[0].Poster != "https://plexmor.example.com/"+testAcc+"/img/own-id/library/metadata/50/thumb/1?w=300&h=450" {
		t.Fatalf("poster through Plexmor: %s", recent[0].Poster)
	}
	img := fetch(t, f.handler, strings.TrimPrefix(recent[0].Poster, "https://plexmor.example.com"), nil)
	if img.Code != http.StatusOK || img.Body.String() != "IMG /library/metadata/50/thumb/1" {
		t.Fatalf("poster: %d %s", img.Code, img.Body)
	}

	genre := decode[struct{ Metas []MetaPreview }](t, fetch(t, f.handler, accPath("/catalog/movie/lib.own-id.1/genre=Familia.json"), nil)).Metas
	if len(genre) != 1 || genre[0].Name != "Boda de Ana" {
		t.Fatalf("genre filter: %+v", genre)
	}
	found := decode[struct{ Metas []MetaPreview }](t, fetch(t, f.handler, accPath("/catalog/series/search/search=juego%20TRONOS.json"), nil)).Metas
	if len(found) != 1 || found[0].ID != "tt0944947" {
		t.Fatalf("search should ignore case and accents: %+v", found)
	}
	if none := decode[struct{ Metas []MetaPreview }](t, fetch(t, f.handler, accPath("/catalog/movie/search/search=zzz.json"), nil)).Metas; len(none) != 0 {
		t.Fatalf("no results expected: %+v", none)
	}

	meta := decode[struct{ Meta Meta }](t, fetch(t, f.handler, accPath("/meta/movie/plexmor:own-id:50.json"), nil)).Meta
	if meta.Name != "Boda de Ana" || meta.Description != "Vídeo de la boda" || meta.Background == "" {
		t.Fatalf("meta: %+v", meta)
	}
	series := decode[struct{ Meta Meta }](t, fetch(t, f.handler, accPath("/meta/series/plexmor:own-id:300.json"), nil)).Meta
	if len(series.Videos) != 1 || series.Videos[0].ID != "plexmor:own-id:301" || series.Videos[0].Season != 1 || series.Videos[0].Episode != 7 {
		t.Fatalf("series meta: %+v", series)
	}
	streams := decode[struct{ Streams []Stream }](t, fetch(t, f.handler, accPath("/stream/movie/plexmor:own-id:50.json"), nil)).Streams
	if len(streams) != 1 || !strings.Contains(streams[0].URL, "/library/parts/50/") {
		t.Fatalf("streams by Plexmor id: %+v", streams)
	}
}

func TestHiddenCatalogs(t *testing.T) {
	f := featureFixture(t, accounts.ModeAuto, func(p *accounts.Prefs) { p.HideCatalogs = true })
	m := decode[manifest](t, fetch(t, f.handler, accPath("/manifest.json"), nil))
	if len(m.Catalogs) != 0 {
		t.Fatalf("catalogs should be hidden: %+v", m.Catalogs)
	}
}

func TestOnDeck(t *testing.T) {
	f := newFixture(t)
	f.own.OnDeck = []plex.Metadata{{RatingKey: "301", Type: "episode", GrandparentRatingKey: "300"}}
	ctx := context.Background()
	_ = f.reg.Refresh(ctx)
	f.reg.Probe(ctx)
	f.syncAll(t)
	got := decode[struct{ Metas []MetaPreview }](t, fetch(t, f.handler, accPath("/catalog/series/ondeck.json"), nil)).Metas
	if len(got) != 1 || got[0].ID != "tt0944947" {
		t.Fatalf("on deck should show the series being watched: %+v", got)
	}
}

func TestExternalSubtitles(t *testing.T) {
	f := featureFixture(t, accounts.ModeDirect, nil)
	subs := decode[struct{ Subtitles []Subtitle }](t, fetch(t, f.handler, accPath("/subtitles/movie/plexmor:own-id:50.json"), nil)).Subtitles
	want := "https://plexmor.example.com/" + testAcc + "/play/own-id/library/streams/777"
	if len(subs) != 1 || subs[0].URL != want || subs[0].Lang != "eng" {
		t.Fatalf("only the external SRT should be offered, through Plexmor: %+v", subs)
	}
	rec := fetch(t, f.handler, strings.TrimPrefix(want, "https://plexmor.example.com"), nil)
	if b, _ := io.ReadAll(rec.Body); string(b) != "CONTENT /library/streams/777" {
		t.Fatalf("subtitle file: %s", b)
	}
}

func TestEachAccountUsesItsOwnAccess(t *testing.T) {
	f := newFixture(t)
	// A friend who can only reach "Casa", with their own token for it.
	f.own.MoreTokens = []string{"casa-for-pepe"}
	f.tv.Accounts = map[string]plextest.Account{"pepe-token": {
		User: plex.User{UUID: "uuid-pepe", Username: "pepe"},
		Resources: []plex.Resource{{Name: "Casa", ClientIdentifier: "own-id", Provides: "server", AccessToken: "casa-for-pepe", SourceTitle: "ana",
			Connections: []plex.Connection{{URI: f.own.URL(), Local: true}}}},
	}}
	if err := f.accs.Put(&accounts.Account{ID: "pepe", PlexToken: "pepe-token", Username: "pepe"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := f.reg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	f.reg.Probe(ctx)
	f.syncAll(t)
	if own := f.reg.Get("own-id"); !own.Owned || own.OwnerAccount != testAcc || own.TokenFor("pepe") != "casa-for-pepe" {
		t.Fatalf("the owner's view must win and each account keeps its token: %+v", own)
	}
	streams := decode[struct{ Streams []Stream }](t, fetch(t, f.handler, "/pepe/stream/movie/tt0816692.json", nil)).Streams
	if len(streams) != 1 || !strings.Contains(streams[0].Description, "Casa") {
		t.Fatalf("pepe only sees Casa: %+v", streams)
	}
	rec := fetch(t, f.handler, strings.TrimPrefix(streams[0].URL, "https://plexmor.example.com"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("the proxy must use pepe's own token for Casa: %d", rec.Code)
	}
	if rec := fetch(t, f.handler, "/pepe/play/friend-id/library/parts/20/1/file.mkv", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("pepe cannot use a server not shared with him: %d", rec.Code)
	}
	m := decode[manifest](t, fetch(t, f.handler, "/pepe/manifest.json", nil))
	for _, c := range m.Catalogs {
		if strings.Contains(c.ID, "friend-id") {
			t.Fatalf("pepe must not see the libraries of servers he cannot use: %s", c.ID)
		}
	}
}

func TestProxyResumesWhenPlexDropsTheStream(t *testing.T) {
	resumeDelay = time.Millisecond
	f := featureFixture(t, accounts.ModeAuto, nil)
	content := make([]byte, 3<<20)
	for i := range content {
		content[i] = byte(i * 7 % 251)
	}
	f.own.PartContent, f.own.CutAfter = content, 700_000
	path := accPath("/play/own-id/library/parts/10/1/file.mkv")

	f.own.CutParts.Store(2)
	before := f.own.PartRequests.Load()
	rec := fetch(t, f.handler, path, nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatalf("the whole file must arrive despite two drops: %d, %d of %d bytes", rec.Code, rec.Body.Len(), len(content))
	}
	if got := f.own.PartRequests.Load() - before; got != 3 {
		t.Fatalf("expected the first request and two resumes, got %d requests", got)
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatal("reverse proxies must not buffer the video")
	}

	f.own.CutParts.Store(1)
	rec = fetch(t, f.handler, path, map[string]string{"Range": "bytes=100000-2999999"})
	if rec.Code != http.StatusPartialContent || !bytes.Equal(rec.Body.Bytes(), content[100000:3000000]) {
		t.Fatalf("a range must be resumed from where it stopped: %d, %d bytes", rec.Code, rec.Body.Len())
	}

	// A server that keeps dropping the stream is given up on.
	f.own.CutParts.Store(100)
	f.own.CutAfter = 100_000
	before = f.own.PartRequests.Load()
	rec = fetch(t, f.handler, path, nil)
	if rec.Body.Len() >= len(content) || f.own.PartRequests.Load()-before != 1+maxResumes {
		t.Fatalf("give up after %d resumes: %d bytes, %d requests", maxResumes, rec.Body.Len(), f.own.PartRequests.Load()-before)
	}
	f.own.CutParts.Store(0)
}
