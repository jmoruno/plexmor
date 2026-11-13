// Package plex talks to plex.tv and to Plex Media Servers, always asking for JSON.
package plex

// Resource is a device returned by plex.tv /api/v2/resources (only servers are used).
type Resource struct {
	Name             string       `json:"name"`
	ClientIdentifier string       `json:"clientIdentifier"`
	Provides         string       `json:"provides"`
	Owned            bool         `json:"owned"`
	AccessToken      string       `json:"accessToken"`
	SourceTitle      string       `json:"sourceTitle"`
	Presence         bool         `json:"presence"`
	Connections      []Connection `json:"connections"`
}

// Connection is one way of reaching a server.
type Connection struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	URI      string `json:"uri"`
	Local    bool   `json:"local"`
	Relay    bool   `json:"relay"`
	IPv6     bool   `json:"IPv6"`
}

// IsServer reports whether the resource is a Plex Media Server.
func (r Resource) IsServer() bool {
	for _, p := range splitComma(r.Provides) {
		if p == "server" {
			return true
		}
	}
	return false
}

// mediaContainer is the envelope of every Plex Media Server JSON response.
type mediaContainer[T any] struct {
	MediaContainer T `json:"MediaContainer"`
}

// Identity is the answer of /identity.
type Identity struct {
	MachineIdentifier string `json:"machineIdentifier"`
	Version           string `json:"version"`
}

// Section is a library (/library/sections).
type Section struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Title string `json:"title"`
	UUID  string `json:"uuid"`
}

type sectionList struct {
	Directory []Section `json:"Directory"`
}

type metadataList struct {
	Size      int        `json:"size"`
	TotalSize int        `json:"totalSize"`
	Metadata  []Metadata `json:"Metadata"`
}

// Metadata is a movie, show or episode.
type Metadata struct {
	RatingKey string `json:"ratingKey"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	UpdatedAt int64  `json:"updatedAt"`
	// LegacyGUID is the single guid of libraries using Plex's legacy agents
	// (com.plexapp.agents.imdb://tt0816692?lang=en); modern agents fill Guids instead.
	LegacyGUID           string  `json:"guid"`
	GrandparentRatingKey string  `json:"grandparentRatingKey"`
	GrandparentTitle     string  `json:"grandparentTitle"`
	ParentIndex          int     `json:"parentIndex"`
	Index                int     `json:"index"`
	Guids                []Guid  `json:"Guid"`
	Media                []Media `json:"Media"`
	// Artwork and descriptive fields, used by catalogs and meta.
	Thumb                 string `json:"thumb"`
	Art                   string `json:"art"`
	GrandparentThumb      string `json:"grandparentThumb"`
	Summary               string `json:"summary"`
	AddedAt               int64  `json:"addedAt"`
	Duration              int64  `json:"duration"`
	OriginallyAvailableAt string `json:"originallyAvailableAt"`
	ViewOffset            int64  `json:"viewOffset"`
	Genres                []Tag  `json:"Genre"`
}

// Tag is a genre, director... entry.
type Tag struct {
	Tag string `json:"tag"`
}

// Guid is an external identifier such as imdb://tt0816692.
type Guid struct {
	ID string `json:"id"`
}

// Media is one version of an item.
type Media struct {
	ID              int64  `json:"id"`
	Duration        int64  `json:"duration"`
	Bitrate         int    `json:"bitrate"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	VideoResolution string `json:"videoResolution"`
	VideoCodec      string `json:"videoCodec"`
	AudioCodec      string `json:"audioCodec"`
	AudioChannels   int    `json:"audioChannels"`
	Container       string `json:"container"`
	Parts           []Part `json:"Part"`
}

// Part is a file of a version.
type Part struct {
	ID        int64    `json:"id"`
	Key       string   `json:"key"`
	File      string   `json:"file"`
	Size      int64    `json:"size"`
	Container string   `json:"container"`
	Streams   []Stream `json:"Stream"`
}

// Stream is a video, audio or subtitle track of a part (only in /library/metadata).
type Stream struct {
	StreamType           int    `json:"streamType"`
	Codec                string `json:"codec"`
	Language             string `json:"language"`
	LanguageCode         string `json:"languageCode"`
	LanguageTag          string `json:"languageTag"`
	Title                string `json:"title"`
	DisplayTitle         string `json:"displayTitle"`
	ExtendedDisplayTitle string `json:"extendedDisplayTitle"`
	Channels             int    `json:"channels"`
	Forced               bool   `json:"forced"`
	Key                  string `json:"key"` // only external (sidecar) subtitles have one
	Format               string `json:"format"`
	DOVIPresent          bool   `json:"DOVIPresent"`
	ColorTrc             string `json:"colorTrc"`
}

// Stream types used by Plex.
const (
	StreamVideo    = 1
	StreamAudio    = 2
	StreamSubtitle = 3
)

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

// Pin is a plex.tv login PIN (/api/v2/pins).
type Pin struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	AuthToken string `json:"authToken"`
}

// User is the account behind a token (/api/v2/user).
type User struct {
	ID       int64  `json:"id"`
	UUID     string `json:"uuid"`
	Username string `json:"username"`
	Title    string `json:"title"`
	Email    string `json:"email"`
	Thumb    string `json:"thumb"`
}

// Name is how the user is shown.
func (u User) Name() string {
	if u.Title != "" {
		return u.Title
	}
	return u.Username
}
