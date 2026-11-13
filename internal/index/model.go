// Package index keeps every movie and episode of every server in memory, ready to answer
// Stremio without talking to Plex. Lookups are lock-free map reads on an immutable snapshot.
package index

import (
	"strings"

	"github.com/jmoruno/plexmor/internal/plex"
)

// IDs are the external identifiers of an item.
type IDs struct {
	IMDb string // tt0816692
	TMDB string // 157336
	TVDB string // 81189
}

// Keys returns the lookup keys of the identifiers ("imdb:tt0816692", "tmdb:157336"...).
func (ids IDs) Keys() []string {
	var keys []string
	if ids.IMDb != "" {
		keys = append(keys, "imdb:"+ids.IMDb)
	}
	if ids.TMDB != "" {
		keys = append(keys, "tmdb:"+ids.TMDB)
	}
	if ids.TVDB != "" {
		keys = append(keys, "tvdb:"+ids.TVDB)
	}
	return keys
}

// ParseIDs extracts the external identifiers from the Plex GUIDs of an item. Modern agents
// list them in Guid[]; legacy agents only have one guid such as
// com.plexapp.agents.imdb://tt0816692?lang=en or com.plexapp.agents.thetvdb://81189/1/5?lang=en.
func ParseIDs(guids []plex.Guid, legacy string) IDs {
	var ids IDs
	for _, g := range guids {
		ids.set(g.ID)
	}
	if ids == (IDs{}) && legacy != "" {
		ids.set(legacy)
	}
	return ids
}

// ParseGuids extracts the external identifiers from modern Plex GUIDs.
func ParseGuids(guids []plex.Guid) IDs { return ParseIDs(guids, "") }

func (ids *IDs) set(guid string) {
	scheme, value, ok := strings.Cut(guid, "://")
	if !ok {
		return
	}
	value, _, _ = strings.Cut(value, "?")
	value, _, _ = strings.Cut(value, "/") // legacy TVDB guids carry /season/episode
	if value == "" {
		return
	}
	switch strings.TrimPrefix(scheme, "com.plexapp.agents.") {
	case "imdb":
		if strings.HasPrefix(value, "tt") {
			ids.IMDb = value
		}
	case "tmdb", "themoviedb":
		ids.TMDB = value
	case "tvdb", "thetvdb":
		ids.TVDB = value
	}
}

// Audio is an audio track.
type Audio struct {
	Language string // canonical name, see Language()
	Codec    string
	Channels int
}

// Subtitle is a subtitle track.
type Subtitle struct {
	Language string
	Forced   bool
	Codec    string // srt, ass, pgs...
	Key      string // /library/streams/123 for external (sidecar) files, empty when embedded
}

// Version is one playable file of an item.
type Version struct {
	PartKey    string // /library/parts/123/1700000000/file.mkv
	MediaIndex int    // position of the version among the Media of the item (for transcoding)
	Part       int    // 1-based part number when the version is split in several files (CD1, CD2)
	Parts      int    // number of parts of the version (0 when it is a single file)
	File       string // base name, used by AIOStreams to parse
	Container  string
	Size       int64
	Bitrate    int // kbps
	Duration   int64
	Resolution string // 4k, 1080, 720, sd...
	Width      int
	Height     int
	VideoCodec string
	HDR        string // DV, HDR10, HLG or ""
	Audio      []Audio
	Subtitles  []Subtitle
	// Detailed is false until the tracks have been read from /library/metadata.
	Detailed bool
}

// Item is a movie or an episode on one server.
type Item struct {
	Server    string // machine identifier
	Section   string // library key
	RatingKey string
	Type      string // movie or episode
	Title     string
	Year      int
	UpdatedAt int64
	IDs       IDs
	// Episodes only.
	ShowKey  string
	Show     string
	Season   int
	Episode  int
	Versions []Version
	// Descriptive fields for catalogs and meta.
	Thumb    string // Plex image path (/library/metadata/10/thumb/1700000000)
	Art      string
	Summary  string
	Genres   []string
	AddedAt  int64
	Released string // 2014-11-05
	Duration int64  // ms
}

// Show is a series on one server (episodes find their series ids through it).
type Show struct {
	RatingKey string
	Section   string
	Title     string
	IDs       IDs
	Year      int
	Thumb     string
	Art       string
	Summary   string
	Genres    []string
	AddedAt   int64
}

// Section is a library of a server.
type Section struct {
	Key   string
	Title string
	Type  string // movie or show
}

// ServerData is everything indexed from one server.
type ServerData struct {
	MachineID string
	Name      string
	Owner     string
	Owned     bool
	Items     map[string]*Item // by ratingKey
	Shows     map[string]*Show // by ratingKey
	Sections  []Section
	SyncedAt  int64 // unix seconds of the last successful sync
}

// NewServerData returns an empty server.
func NewServerData(machineID string) *ServerData {
	return &ServerData{MachineID: machineID, Items: map[string]*Item{}, Shows: map[string]*Show{}}
}

// EpisodeKey is the lookup key of an episode by series id, season and episode: "imdb:tt0944947:1:5".
func EpisodeKey(seriesKey string, season, episode int) string {
	return seriesKey + ":" + itoa(season) + ":" + itoa(episode)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
