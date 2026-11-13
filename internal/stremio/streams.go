package stremio

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/jmoruno/plexmor/internal/accounts"
	"github.com/jmoruno/plexmor/internal/i18n"
	"github.com/jmoruno/plexmor/internal/index"
	"github.com/jmoruno/plexmor/internal/servers"
)

// Stream is a Stremio stream object.
type Stream struct {
	Name string `json:"name"`
	// Description only: Stremio reads "title" as an alias of it and rejects the whole
	// response when both are present.
	Description   string        `json:"description"`
	URL           string        `json:"url"`
	BehaviorHints BehaviorHints `json:"behaviorHints"`
}

// BehaviorHints help Stremio and AIOStreams handle the stream.
type BehaviorHints struct {
	BingeGroup  string `json:"bingeGroup,omitempty"`
	Filename    string `json:"filename,omitempty"`
	VideoSize   int64  `json:"videoSize,omitempty"`
	NotWebReady bool   `json:"notWebReady,omitempty"`
}

// StreamOptions are the per-request settings of BuildStreams.
type StreamOptions struct {
	AccountID   string
	Languages   []string
	MaxStreams  int
	Resolutions []string // allowed resolution labels (4k, 1440, 1080, 720, sd); empty = all
	Mode        string   // accounts.ModeAuto, ModeProxy or ModeDirect
	BaseURL     string   // public base URL of Plexmor (https://plexmor.example.com); empty disables the proxy
	Transcode   bool     // add 1080p and 720p versions transcoded by Plex
	Lang        string   // language of the texts: i18n.ES or i18n.EN
	// Allowed filters servers and libraries (nil = everything).
	Allowed func(machineID, section string) bool
}

type candidate struct {
	item      *index.Item
	version   index.Version
	server    *servers.Server
	token     string
	lang      int
	relay     bool
	res       int
	hdr       int
	transcode int // 0 = original, otherwise the target height
}

// BuildStreams turns indexed items into sorted Stremio streams. Items on unreachable servers,
// or on servers the account cannot use, are skipped.
func BuildStreams(items []*index.Item, lookup func(machineID string) *servers.Server, o StreamOptions) []Stream {
	allowedRes := map[int]bool{}
	for _, r := range o.Resolutions {
		allowedRes[resolutionRank(index.Version{Resolution: r})] = true
	}
	var cands []candidate
	for _, it := range items {
		srv := lookup(it.Server)
		if srv == nil || !srv.Healthy() {
			continue
		}
		token := srv.TokenFor(o.AccountID)
		if token == "" || (o.Allowed != nil && !o.Allowed(it.Server, it.Section)) {
			continue
		}
		for _, v := range it.Versions {
			c := candidate{
				item: it, version: v, server: srv, token: token,
				lang:  languageRank(v, o.Languages),
				relay: srv.Best.Relay,
				res:   resolutionRank(v),
				hdr:   hdrRank(v.HDR),
			}
			if len(allowedRes) == 0 || allowedRes[c.res] {
				cands = append(cands, c)
			}
			if o.Transcode && v.Part <= 1 {
				for _, target := range []int{1080, 720} {
					if c.res <= resolutionRank(index.Version{Height: target}) {
						continue // only lower qualities than the original
					}
					t := c
					t.transcode = target
					t.res = resolutionRank(index.Version{Height: target})
					t.hdr = 0
					cands = append(cands, t)
				}
			}
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		switch {
		case a.lang != b.lang:
			return a.lang < b.lang
		case a.relay != b.relay:
			// The Plex relay is capped at 2-10 Mbps: any direct connection plays better.
			return !a.relay
		case (a.transcode == 0) != (b.transcode == 0):
			return a.transcode == 0 // originals before transcoded versions
		case a.res != b.res:
			return a.res > b.res
		case a.hdr != b.hdr:
			return a.hdr > b.hdr
		case a.version.Bitrate != b.version.Bitrate:
			return a.version.Bitrate > b.version.Bitrate
		case a.server.Best.Latency != b.server.Best.Latency:
			return a.server.Best.Latency < b.server.Best.Latency
		case a.server.MachineID != b.server.MachineID:
			return a.server.MachineID < b.server.MachineID
		default:
			// Stable order between responses (the index is built from maps).
			return a.version.PartKey < b.version.PartKey
		}
	})
	if o.MaxStreams > 0 && len(cands) > o.MaxStreams {
		cands = cands[:o.MaxStreams]
	}
	out := make([]Stream, 0, len(cands))
	for _, c := range cands {
		out = append(out, toStream(c, o))
	}
	return out
}

// languageRank is the position of the best preferred language present (lower is better).
func languageRank(v index.Version, prefs []string) int {
	best := len(prefs) + 1
	if !v.Detailed {
		return best // tracks not read yet: after every version with a known preferred language
	}
	for _, a := range v.Audio {
		for i, p := range prefs {
			if a.Language == p && i < best {
				best = i
			}
		}
	}
	if best == len(prefs)+1 {
		return len(prefs) // known tracks but none preferred
	}
	return best
}

// resolutionRank orders resolutions: 4K 5, 1440p 4, 1080p 3, 720p 2, SD 1, unknown 0.
func resolutionRank(v index.Version) int {
	switch strings.ToLower(v.Resolution) {
	case "4k", "2160":
		return 5
	case "1440", "2k":
		return 4
	case "1080":
		return 3
	case "720":
		return 2
	case "":
	default:
		return 1
	}
	switch {
	case v.Height >= 1800 || v.Width >= 3200:
		return 5
	case v.Height >= 1300 || v.Width >= 2400:
		return 4
	case v.Height >= 1000 || v.Width >= 1800:
		return 3
	case v.Height >= 700 || v.Width >= 1200:
		return 2
	case v.Height > 0:
		return 1
	}
	return 0
}

func hdrRank(hdr string) int {
	switch hdr {
	case "DV":
		return 3
	case "HDR10":
		return 2
	case "HLG":
		return 1
	}
	return 0
}

func resolutionLabel(v index.Version) string {
	switch resolutionRank(v) {
	case 5:
		return "4K"
	case 4:
		return "1440p"
	case 3:
		return "1080p"
	case 2:
		return "720p"
	case 1:
		if r := strings.ToLower(v.Resolution); r != "" && r != "sd" {
			return r + "p"
		}
		return "SD"
	}
	return "?"
}

// useProxy decides whether a stream goes through Plexmor.
func useProxy(o StreamOptions, ep *servers.Endpoint) bool {
	if o.BaseURL == "" {
		return false
	}
	switch o.Mode {
	case accounts.ModeProxy:
		return true
	case accounts.ModeDirect:
		return false
	}
	// Auto: a connection that only works at home is useless for a client away from home.
	return !ep.Relay && ep.HomeOnly()
}

// PlayURL is the address of a Plex path through the Plexmor proxy.
func PlayURL(base, accountID, machineID, plexPath string) string {
	return strings.TrimRight(base, "/") + "/" + accountID + "/play/" + machineID + plexPath
}

func toStream(c candidate, o StreamOptions) Stream {
	v := c.version
	res := resolutionLabel(v)
	if c.transcode > 0 {
		res = resolutionLabel(index.Version{Height: c.transcode})
	}
	quality := res
	if v.HDR != "" && c.transcode == 0 {
		quality += " " + v.HDR
	}
	if c.transcode > 0 {
		quality += " (" + i18n.T(o.Lang, "stream.transcoded") + ")"
	}

	var lines []string
	if c.item.Type == "episode" {
		lines = append(lines, "📺 "+c.item.Show+" · "+i18n.T(o.Lang, "stream.episode", c.item.Season, c.item.Episode)+" · "+c.item.Title)
	}
	video := []string{res}
	if c.transcode > 0 {
		video = append(video, "H264", i18n.T(o.Lang, "stream.from", resolutionLabel(v)))
	} else {
		if v.HDR != "" {
			video = append(video, v.HDR)
		}
		if v.VideoCodec != "" {
			video = append(video, strings.ToUpper(v.VideoCodec))
		}
		if v.Size > 0 {
			video = append(video, humanSize(v.Size))
		}
	}
	if v.Parts > 1 {
		video = append(video, i18n.T(o.Lang, "stream.part", v.Part, v.Parts))
	}
	lines = append(lines, "🎬 "+strings.Join(video, " · "))
	if len(v.Audio) > 0 {
		audio := make([]string, 0, len(v.Audio))
		for _, a := range v.Audio {
			label := index.LanguageName(a.Language, o.Lang)
			if ch := channels(a.Channels); ch != "" {
				label += " " + ch
			}
			audio = append(audio, label)
		}
		lines = append(lines, "🔊 "+strings.Join(audio, " · "))
	}
	if len(v.Subtitles) > 0 {
		subs := make([]string, 0, len(v.Subtitles))
		seen := map[string]bool{}
		for _, s := range v.Subtitles {
			label := index.LanguageName(s.Language, o.Lang)
			if s.Forced {
				label += " (" + i18n.T(o.Lang, "stream.forced") + ")"
			}
			if !seen[label] {
				seen[label] = true
				subs = append(subs, label)
			}
		}
		lines = append(lines, "💬 "+strings.Join(subs, ", "))
	}
	ep := c.server.Best
	proxied := useProxy(o, ep)
	route := i18n.T(o.Lang, "conn."+ep.Kind())
	if proxied {
		route += " → Plexmor"
	}
	lines = append(lines, fmt.Sprintf("🖥️ %s · %s · %d ms", c.server.Label(), route, ep.Latency.Milliseconds()))
	desc := strings.Join(lines, "\n")

	plexPath := v.PartKey
	if c.transcode > 0 {
		plexPath = transcodePath(c.item.RatingKey, v.MediaIndex, c.transcode)
	}
	var link string
	if proxied {
		link = PlayURL(o.BaseURL, o.AccountID, c.server.MachineID, plexPath)
	} else {
		sep := "?"
		if strings.Contains(plexPath, "?") {
			sep = "&"
		}
		link = strings.TrimRight(ep.URI, "/") + plexPath + sep + "X-Plex-Token=" + url.QueryEscape(c.token)
	}
	webReady := strings.EqualFold(v.Container, "mp4") && strings.EqualFold(v.VideoCodec, "h264")
	hints := BehaviorHints{
		BingeGroup:  "plexmor|" + c.server.MachineID + "|" + quality,
		NotWebReady: !webReady,
	}
	if c.transcode == 0 {
		hints.Filename, hints.VideoSize = v.File, v.Size
	} else {
		hints.NotWebReady = false // HLS H264, plays everywhere
	}
	return Stream{Name: "Plexmor\n" + quality, Description: desc, URL: link, BehaviorHints: hints}
}

// transcodePath asks Plex for an HLS H264 version of an item at a lower resolution.
func transcodePath(ratingKey string, mediaIndex, height int) string {
	width, bitrate := 1920, 8000
	if height <= 720 {
		width, bitrate = 1280, 4000
	}
	session := "plexmor-" + accounts.RandomID(8)
	q := url.Values{
		"path":                      {"/library/metadata/" + ratingKey},
		"mediaIndex":                {strconv.Itoa(mediaIndex)},
		"partIndex":                 {"0"},
		"protocol":                  {"hls"},
		"fastSeek":                  {"1"},
		"directPlay":                {"0"},
		"directStream":              {"1"},
		"directStreamAudio":         {"1"},
		"videoQuality":              {"100"},
		"videoResolution":           {strconv.Itoa(width) + "x" + strconv.Itoa(height)},
		"maxVideoBitrate":           {strconv.Itoa(bitrate)},
		"subtitles":                 {"none"},
		"session":                   {session},
		"X-Plex-Session-Identifier": {session},
		"X-Plex-Client-Identifier":  {session},
		"X-Plex-Product":            {"Plexmor"},
		"X-Plex-Platform":           {"Chrome"},
	}
	return "/video/:/transcode/universal/start.m3u8?" + q.Encode()
}

func channels(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "1.0"
	case 2:
		return "2.0"
	case 3:
		return "2.1"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return strconv.Itoa(n) + "ch"
}

func humanSize(b int64) string {
	const gib = 1 << 30
	const mib = 1 << 20
	if b >= gib {
		return strconv.FormatFloat(float64(b)/gib, 'f', 1, 64) + " GB"
	}
	return strconv.FormatInt(b/mib, 10) + " MB"
}
