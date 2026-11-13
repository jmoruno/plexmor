// Package i18n holds Plexmor's texts in Spanish and English.
package i18n

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Supported languages.
const (
	ES = "es"
	EN = "en"
)

// Parse maps a language setting (es, en, en-US, es_ES.UTF-8...) to a supported language,
// or "" when it is neither.
func Parse(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case strings.HasPrefix(v, "en"):
		return EN
	case strings.HasPrefix(v, "es"):
		return ES
	}
	return ""
}

// Or returns the supported language of v, or def when v is not one.
func Or(v, def string) string {
	if l := Parse(v); l != "" {
		return l
	}
	return def
}

// FromAcceptLanguage returns the first supported language of an Accept-Language header, or "".
func FromAcceptLanguage(h string) string {
	for _, part := range strings.Split(h, ",") {
		tag, _, _ := strings.Cut(part, ";")
		if l := Parse(tag); l != "" {
			return l
		}
	}
	return ""
}

// T returns the text of key in lang, formatted with args. Unknown keys come back as the key,
// so a missing text is visible instead of silently empty.
func T(lang, key string, args ...any) string {
	m, ok := messages[key]
	if !ok {
		return key
	}
	s := m.es
	if lang == EN {
		s = m.en
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

// Ago says how long ago a unix time was ("hace 5 min", "5 min ago").
func Ago(lang string, unix int64) string {
	if unix <= 0 {
		return T(lang, "ago.never")
	}
	d := time.Since(time.Unix(unix, 0))
	switch {
	case d < time.Minute:
		return T(lang, "ago.now")
	case d < time.Hour:
		return T(lang, "ago.min", strconv.Itoa(int(d.Minutes())))
	case d < 48*time.Hour:
		return T(lang, "ago.hours", strconv.Itoa(int(d.Hours())))
	}
	return T(lang, "ago.days", strconv.Itoa(int(d.Hours()/24)))
}

type text struct{ es, en string }

var messages = map[string]text{
	// Language switch: the name of the other language.
	"lang.other":      {"English", "Español"},
	"lang.other.code": {"en", "es"},

	// Sign in
	"login.plex":        {"Tu Plex", "Your Plex"},
	"login.title":       {"Tu Plex, dentro de Stremio", "Your Plex, inside Stremio"},
	"login.lead":        {"Entra con tu cuenta de Plex para instalar el addon y elegir idiomas, calidades y bibliotecas.", "Sign in with your Plex account to install the addon and choose languages, qualities and libraries."},
	"login.button":      {"Entrar con Plex", "Sign in with Plex"},
	"login.first":       {"Todavía no hay nadie: la cuenta con la que entres será la de administrador.", "Nobody is here yet: the account you sign in with becomes the administrator."},
	"login.wait":        {"Inicia sesión en la ventana de Plex. Esta página se actualizará sola.", "Sign in in the Plex window. This page will refresh by itself."},
	"login.expired":     {"El inicio de sesión ha caducado. Vuelve a pulsar «Entrar con Plex».", "The sign-in expired. Press “Sign in with Plex” again."},
	"login.failed":      {"No se ha podido entrar.", "Could not sign in."},
	"login.unreachable": {"Plex no responde. Inténtalo en un momento.", "Plex is not answering. Try again in a moment."},

	// Header and overview
	"home.logout":     {"Salir", "Sign out"},
	"home.status":     {"Estado", "Status"},
	"home.servers":    {"Tus servidores", "Your servers"},
	"home.owner":      {"de %s", "shared by %s"},
	"home.mine":       {"tuyo", "yours"},
	"home.titles":     {"títulos", "titles"},
	"home.checked":    {"revisado %s", "checked %s"},
	"home.offline":    {"Sin conexión ahora mismo", "Offline right now"},
	"home.noservers":  {"Aún no hay servidores. Si acabas de entrar, espera un minuto y recarga.", "No servers yet. If you just signed in, wait a minute and reload."},
	"home.ready":      {"títulos listos para Stremio", "titles ready for Stremio"},
	"home.addon":      {"Tu dirección personal del addon. Sirve también para AIOStreams; no la compartas.", "Your personal addon address. It also works with AIOStreams; do not share it."},
	"home.addr":       {"Dirección del addon", "Addon address"},
	"home.copy":       {"Copiar", "Copy"},
	"home.copied":     {"Dirección copiada", "Address copied"},
	"home.install":    {"Instalar en la app", "Install in the app"},
	"home.web":        {"Abrir en Stremio Web", "Open in Stremio Web"},
	"home.newurl":     {"Generar dirección nueva", "Generate a new address"},
	"home.newurl.ask": {"Tendrás que volver a instalar el addon en Stremio y en AIOStreams: la dirección actual dejará de funcionar. ¿Generar una nueva?", "You will have to install the addon again in Stremio and AIOStreams: the current address will stop working. Generate a new one?"},

	// Tabs
	"tab.play":  {"Reproducción", "Playback"},
	"tab.libs":  {"Bibliotecas", "Libraries"},
	"tab.users": {"Usuarios", "Users"},

	// Playback settings
	"audio.title":    {"Idiomas de audio", "Audio languages"},
	"audio.text":     {"Las fuentes con el primer idioma de la lista salen arriba. Castellano y latino cuentan como idiomas distintos.", "Sources in the first language of the list come first. Spanish from Spain and Latin American Spanish count as different languages."},
	"audio.up":       {"Subir", "Move up"},
	"audio.down":     {"Bajar", "Move down"},
	"audio.remove":   {"Quitar", "Remove"},
	"audio.add":      {"Añadir idioma…", "Add a language…"},
	"audio.add.aria": {"Añadir idioma", "Add a language"},
	"res.title":      {"Calidades", "Qualities"},
	"res.text":       {"Lo que desmarques no aparecerá en Stremio.", "Anything you untick will not show up in Stremio."},
	"max.title":      {"Fuentes por título", "Sources per title"},
	"max.text":       {"Cuántas versiones enseñar como mucho para cada película o capítulo.", "The most versions to show for each movie or episode."},
	"max.less":       {"Menos", "Fewer"},
	"max.more":       {"Más", "More"},
	"max.unlimited":  {"Sin límite: se enseñan todas.", "No limit: all of them are shown."},
	"mode.title":     {"Cómo llega el vídeo", "How the video reaches you"},
	"mode.text":      {"Plexmor puede servir el vídeo él mismo para que funcione fuera de casa y tu token de Plex no salga de tu red.", "Plexmor can serve the video itself, so it works away from home and your Plex token never leaves your network."},
	"mode.auto":      {"Automático", "Automatic"},
	"mode.auto.text": {"Por Plexmor solo cuando el servidor no es accesible desde fuera. Recomendado.", "Through Plexmor only when the server cannot be reached from outside. Recommended."},
	"mode.proxy":     {"Siempre por Plexmor", "Always through Plexmor"},
	"mode.proxy.text": {"Todo pasa por tu Plexmor, también lo de los servidores de tus amigos.",
		"Everything goes through your Plexmor, your friends' servers included."},
	"mode.direct":      {"Directo a Plex", "Straight to Plex"},
	"mode.direct.text": {"Stremio habla con Plex sin intermediarios. Solo funciona donde Plex sea accesible.", "Stremio talks to Plex with nothing in between. It only works where Plex can be reached."},
	"tc.title":         {"Versiones ligeras", "Lighter versions"},
	"tc.text":          {"Añade copias a 1080p y 720p que Plex convierte al vuelo, para conexiones lentas o el móvil con datos.", "Adds 1080p and 720p copies that Plex converts on the fly, for slow connections or mobile data."},
	"tc.switch":        {"Ofrecer versiones transcodificadas", "Offer transcoded versions"},
	"cat.title":        {"Catálogos en Stremio", "Catalogs in Stremio"},
	"cat.text":         {"Novedades, seguir viendo, tu watchlist de Plex, cada biblioteca y la búsqueda.", "Recently added, continue watching, your Plex watchlist, every library and search."},
	"cat.switch":       {"Mostrar catálogos", "Show catalogs"},

	// Libraries
	"libs.use":     {"Usar %s", "Use %s"},
	"libs.offline": {"sin conexión", "offline"},
	"libs.pending": {"Sus bibliotecas aparecerán cuando se pueda leer.", "Its libraries will show up once it can be read."},
	"libs.none":    {"Aún no hay servidores.", "No servers yet."},
	"kind.movie":   {"Películas", "Movies"},
	"kind.show":    {"Series", "TV shows"},

	// Users
	"users.title":    {"Quién usa este Plexmor", "Who uses this Plexmor"},
	"users.text":     {"Puede entrar cualquiera a quien compartas tu Plex. Cada uno tiene su dirección y sus preferencias.", "Anyone you share your Plex with can sign in. Each person gets their own address and preferences."},
	"users.seen":     {"Último uso %s", "Last used %s"},
	"users.remove":   {"Quitar", "Remove"},
	"users.ask":      {"¿Quitar a %s? Su dirección del addon dejará de funcionar.", "Remove %s? Their addon address will stop working."},
	"users.you":      {"Tú", "You"},
	"users.reindex":  {"Revisar bibliotecas ahora", "Check libraries now"},
	"users.checking": {"Revisando todos los servidores", "Checking every server"},
	"users.metrics":  {"Ver métricas", "View metrics"},

	// Save bar
	"save.region":  {"Cambios sin guardar", "Unsaved changes"},
	"save.text":    {"Tienes cambios sin guardar", "You have unsaved changes"},
	"save.discard": {"Descartar", "Discard"},
	"save.save":    {"Guardar cambios", "Save changes"},
	"save.done":    {"Cambios guardados", "Changes saved"},

	// Relative times
	"ago.never": {"nunca", "never"},
	"ago.now":   {"ahora mismo", "just now"},
	"ago.min":   {"hace %s min", "%s min ago"},
	"ago.hours": {"hace %s h", "%s h ago"},
	"ago.days":  {"hace %s días", "%s days ago"},

	// Errors of the panel API
	"err.origin":   {"origen no permitido", "origin not allowed"},
	"err.session":  {"inicia sesión de nuevo", "sign in again"},
	"err.admin":    {"solo para administradores", "administrators only"},
	"err.plextv":   {"plex.tv no responde, inténtalo en un momento", "plex.tv is not answering, try again in a moment"},
	"err.busy":     {"hay demasiados inicios de sesión a la vez, inténtalo en un momento", "too many sign-ins at once, try again in a moment"},
	"err.expired":  {"el inicio de sesión ha caducado, vuelve a empezar", "the sign-in expired, start again"},
	"err.noaccess": {"tu cuenta de Plex no tiene acceso a ningún servidor de este Plexmor; pide al administrador que te comparta su Plex", "your Plex account cannot reach any server of this Plexmor; ask the administrator to share their Plex with you"},
	"err.account":  {"no se pudo leer tu cuenta de Plex", "could not read your Plex account"},
	"err.form":     {"formulario no válido", "invalid form"},
	"err.save":     {"no se pudo guardar", "could not save"},
	"err.change":   {"no se pudo cambiar", "could not change it"},
	"err.self":     {"no puedes quitar tu propia cuenta", "you cannot remove your own account"},
	"err.delete":   {"no se pudo quitar", "could not remove it"},
	"err.trakt.off": {"Trakt no está configurado en este Plexmor",
		"Trakt is not set up in this Plexmor"},
	"err.trakt.down":    {"Trakt no responde", "Trakt is not answering"},
	"err.trakt.link":    {"no se pudo completar la conexión con Trakt", "could not finish connecting to Trakt"},
	"err.trakt.vip":     {"Conectado, pero tu cuenta de Trakt no es VIP: el módulo de Trakt solo funciona con VIP", "Connected, but your Trakt account is not VIP: the Trakt module only works with VIP"},
	"err.trakt.account": {"no se pudo leer tu cuenta de Trakt", "could not read your Trakt account"},
	"err.trakt.unlink":  {"no se pudo desconectar", "could not disconnect"},

	// Stremio
	"addon.desc":        {"Tu servidor Plex y los que te comparten, con fuentes detalladas y tus idiomas primero.", "Your Plex server and the ones shared with you, with detailed sources and your languages first."},
	"addon.account":     {" Cuenta: %s.", " Account: %s."},
	"cat.recent":        {"Plexmor · Novedades", "Plexmor · Recently added"},
	"cat.ondeck":        {"Plexmor · Seguir viendo", "Plexmor · Continue watching"},
	"cat.watchlist":     {"Plexmor · Watchlist de Plex", "Plexmor · Plex Watchlist"},
	"cat.trakt.watch":   {"Trakt · Watchlist", "Trakt · Watchlist"},
	"cat.trakt.rec":     {"Trakt · Recomendado", "Trakt · Recommended"},
	"stream.transcoded": {"transcodificado", "transcoded"},
	"stream.from":       {"desde %s", "from %s"},
	"stream.part":       {"parte %d de %d", "part %d of %d"},
	"stream.forced":     {"forzados", "forced"},
	"stream.episode":    {"T%dE%02d", "S%dE%02d"},
	"conn.local":        {"Local", "Local"},
	"conn.remote":       {"Remota", "Remote"},
	"conn.relay":        {"Relay", "Relay"},
}
