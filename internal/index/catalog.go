package index

import (
	"sort"
	"strings"
)

// Entry is a movie or a series as shown in a catalog. Series group their episodes.
type Entry struct {
	Kind      string // movie or series
	Server    string
	Section   string
	RatingKey string
	Title     string
	Year      int
	IDs       IDs
	Thumb     string
	Art       string
	Summary   string
	Genres    []string
	// AddedAt is when the movie was added, or the newest episode of the series.
	AddedAt int64
	norm    string // folded title for search
}

// ID is the Stremio id of the entry: the IMDb id when known (so Cinemeta and every other
// addon recognize it), otherwise a Plexmor id that points to this server and item.
func (e *Entry) ID() string {
	if e.IDs.IMDb != "" {
		return e.IDs.IMDb
	}
	return PlexmorID(e.Server, e.RatingKey)
}

// PlexmorID is the Stremio id of an item that has no IMDb id: "plexmor:{machine}:{ratingKey}".
func PlexmorID(server, ratingKey string) string { return "plexmor:" + server + ":" + ratingKey }

// ParsePlexmorID splits a Plexmor id.
func ParsePlexmorID(id string) (server, ratingKey string, ok bool) {
	rest, found := strings.CutPrefix(id, "plexmor:")
	if !found {
		return "", "", false
	}
	server, ratingKey, ok = strings.Cut(rest, ":")
	return server, ratingKey, ok && server != "" && ratingKey != ""
}

func buildEntries(s *snapshot) {
	var entries []*Entry
	for id, sd := range s.servers {
		newest := map[string]int64{}
		for _, it := range sd.Items {
			if it.Type == "episode" {
				if it.AddedAt > newest[it.ShowKey] {
					newest[it.ShowKey] = it.AddedAt
				}
				continue
			}
			entries = append(entries, &Entry{
				Kind: "movie", Server: id, Section: it.Section, RatingKey: it.RatingKey, Title: it.Title, Year: it.Year,
				IDs: it.IDs, Thumb: it.Thumb, Art: it.Art, Summary: it.Summary, Genres: it.Genres, AddedAt: it.AddedAt,
				norm: Fold(it.Title),
			})
		}
		for _, sh := range sd.Shows {
			added := max(sh.AddedAt, newest[sh.RatingKey])
			entries = append(entries, &Entry{
				Kind: "series", Server: id, Section: sh.Section, RatingKey: sh.RatingKey, Title: sh.Title, Year: sh.Year,
				IDs: sh.IDs, Thumb: sh.Thumb, Art: sh.Art, Summary: sh.Summary, Genres: sh.Genres, AddedAt: added,
				norm: Fold(sh.Title),
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].AddedAt != entries[j].AddedAt {
			return entries[i].AddedAt > entries[j].AddedAt
		}
		if entries[i].Server != entries[j].Server {
			return entries[i].Server < entries[j].Server
		}
		return entries[i].RatingKey < entries[j].RatingKey
	})
	s.entries = entries
	for _, eps := range s.episodes {
		sort.Slice(eps, func(i, j int) bool {
			if eps[i].Season != eps[j].Season {
				return eps[i].Season < eps[j].Season
			}
			return eps[i].Episode < eps[j].Episode
		})
	}
}

// Entries returns every movie and series, newest first. The slice must not be modified.
func (s *Store) Entries() []*Entry { return s.snap.Load().entries }

// Episodes returns the episodes of a series on a server, in order.
func (s *Store) Episodes(server, showKey string) []*Item {
	return s.snap.Load().episodes[server+"/"+showKey]
}

// Item returns an item of a server by ratingKey, or nil.
func (s *Store) Item(server, ratingKey string) *Item {
	if sd := s.snap.Load().servers[server]; sd != nil {
		return sd.Items[ratingKey]
	}
	return nil
}

// Show returns a series of a server by ratingKey, or nil.
func (s *Store) Show(server, ratingKey string) *Show {
	if sd := s.snap.Load().servers[server]; sd != nil {
		return sd.Shows[ratingKey]
	}
	return nil
}

// Matches reports whether the entry matches a folded search query: every word of the
// query must appear in the title.
func (e *Entry) Matches(foldedQuery string) bool {
	for _, w := range strings.Fields(foldedQuery) {
		if !strings.Contains(e.norm, w) {
			return false
		}
	}
	return foldedQuery != ""
}

// SearchRank orders search results: titles that start with the query first.
func (e *Entry) SearchRank(foldedQuery string) int {
	switch {
	case e.norm == foldedQuery:
		return 0
	case strings.HasPrefix(e.norm, foldedQuery):
		return 1
	}
	return 2
}

var folder = strings.NewReplacer(
	"á", "a", "à", "a", "ä", "a", "â", "a", "ã", "a",
	"é", "e", "è", "e", "ë", "e", "ê", "e",
	"í", "i", "ì", "i", "ï", "i", "î", "i",
	"ó", "o", "ò", "o", "ö", "o", "ô", "o", "õ", "o",
	"ú", "u", "ù", "u", "ü", "u", "û", "u",
	"ñ", "n", "ç", "c",
	":", " ", "-", " ", "·", " ", ".", " ", ",", " ", "'", "", "’", "", "!", "", "?", "", "¿", "", "¡", "",
)

// Fold lowercases a title and removes accents and punctuation for search.
func Fold(s string) string {
	return strings.Join(strings.Fields(folder.Replace(strings.ToLower(s))), " ")
}
