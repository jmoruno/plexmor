<p align="center">
  <img src="internal/panel/static/logo.svg" width="128" height="128" alt="Plexmor">
</p>

<h1 align="center">Plexmor</h1>

<p align="center"><em>Tu Plex y los que te comparten tus amigos, dentro de Stremio.</em></p>

<p align="center">
  <a href="https://github.com/jmoruno/plexmor/actions/workflows/ci.yml"><img src="https://github.com/jmoruno/plexmor/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
  <a href="https://github.com/jmoruno/plexmor/actions/workflows/docker.yml"><img src="https://github.com/jmoruno/plexmor/actions/workflows/docker.yml/badge.svg?branch=main" alt="Build"></a>
  <a href="https://github.com/jmoruno/plexmor/actions/workflows/codeql.yml"><img src="https://github.com/jmoruno/plexmor/actions/workflows/codeql.yml/badge.svg?branch=main" alt="CodeQL"></a>
  <a href="https://github.com/jmoruno/plexmor/actions/workflows/trivy.yml"><img src="https://github.com/jmoruno/plexmor/actions/workflows/trivy.yml/badge.svg?branch=main" alt="Trivy"></a>
  <a href="https://github.com/jmoruno/plexmor/releases/latest"><img src="https://img.shields.io/github/v/release/jmoruno/plexmor" alt="Release"></a>
  <a href="https://hub.docker.com/r/jmorunofdz/plexmor"><img src="https://img.shields.io/docker/pulls/jmorunofdz/plexmor" alt="Docker pulls"></a>
  <a href="https://hub.docker.com/r/jmorunofdz/plexmor"><img src="https://img.shields.io/docker/image-size/jmorunofdz/plexmor/stable" alt="Image size"></a>
  <a href="https://github.com/jmoruno/plexmor/blob/main/LICENSE"><img src="https://img.shields.io/github/license/jmoruno/plexmor" alt="License"></a>
  <br>
  <a href="https://buymeacoffee.com/jmoruno"><img src="https://img.shields.io/badge/Buy%20me%20a%20coffee-FFDD00?logo=buymeacoffee&logoColor=black" alt="Buy Me a Coffee"></a>
</p>

<p align="center"><b>Español</b> · <a href="README.en.md">English</a></p>

Addon de Stremio (compatible con Nuvio y AIOStreams) para ver tu servidor Plex y los que te comparten tus amigos, con panel web y varios usuarios.
Responde desde un índice propio en memoria, así que Stremio nunca espera a ningún servidor Plex.

- **Fuentes:** todas las versiones de todos tus servidores, ordenadas por idioma (castellano y latino por separado), resolución, HDR y latencia, con códec, audios, subtítulos y tamaño.
- **Fuera de casa:** en modo automático, si un servidor solo es accesible desde tu red, el vídeo pasa por Plexmor y el token de Plex nunca sale de casa. También hay modo «siempre por Plexmor» y «directo a Plex».
- **Catálogos:** novedades, seguir viendo, watchlist de Plex, cada biblioteca (con filtro por género) y búsqueda en todos los servidores.
- **Subtítulos externos** y **versiones ligeras** opcionales (1080p y 720p transcodificadas por Plex).
- **Panel web en castellano o inglés:** entrar con Plex, instalar en Stremio, idiomas, calidades, bibliotecas y usuarios.
- **Varios usuarios:** entra quien tenga acceso a tu Plex; cada uno tiene su dirección, sus preferencias y su propio acceso a cada servidor.
- **Métricas** en `/metrics` (formato Prometheus).

## Idioma

| Variable | Por defecto | Para qué |
| --- | --- | --- |
| `LANGUAGE` | `es` | Idioma del panel y de los textos en Stremio: `es` o `en`. Cada usuario puede cambiarlo en el panel |
| `LANGUAGES` | según `LANGUAGE` | Idiomas de audio por defecto, el mejor primero: `castellano,latino,ingles` con `es` e `ingles` con `en`. Valen también nombres en inglés (`english`, `japanese`…) |

## Instalar (Portainer o docker compose)

📘 **Guía paso a paso: [Instalar Plexmor con Portainer](docs/portainer.es.md)**: carpetas, rutas de Plex según la instalación, usuario, despliegue, HTTPS y problemas frecuentes.

En resumen:

1. Stack con [`docker-compose.yml`](docker-compose.yml) (imagen `jmorunofdz/plexmor:stable`).
2. Variables del stack: las de [`.env.example`](.env.example). Lo mínimo es `DATA_PATH`; con `PLEX_CONFIG` el dueño de Plex queda como administrador sin escribir tokens.
3. Abre el panel (`http://<servidor>:7254/` o tu dominio), entra con Plex y pulsa **Instalar en la app** o **Abrir en Stremio Web**.

Para usarlo fuera de casa, publícalo con HTTPS (por ejemplo con Nginx Proxy Manager). Stremio Web exige HTTPS.

| Variable | Por defecto | Qué es |
| --- | --- | --- |
| `DATA_PATH` | `./data` | Carpeta del host para el índice, las cuentas y el secreto (en Unraid, una carpeta de `appdata`) |
| `PLEX_CONFIG` | — | Carpeta de Plex que contiene `Preferences.xml` (solo lectura). El dueño del servidor queda como administrador sin escribir ningún token |
| `PLEX_TOKEN` | — | Alternativa a `PLEX_CONFIG` si Plex está en otra máquina. Sin ninguno de los dos, quien entre primero en el panel es el administrador |
| `PUBLIC_URL` | — | Dirección pública (`https://plexmor.example.com`). Si no se pone, se toma de cada petición |
| `PLEXMOR_SECRET` | aleatorio | Cifra los tokens guardados y firma las sesiones. Si no se pone, se genera en `/data/secret` |
| `METRICS_TOKEN` | — | Permite a Prometheus leer `/metrics` con `Authorization: Bearer <token>`. Sin él, solo los administradores del panel |
| `PLEXMOR_PORT` | `7254` | Puerto publicado |
| `SERVERS` | todos | Limitar a estos servidores (por nombre) |
| `PUID` / `PGID` | `99` / `100` | Usuario del contenedor (el de Unraid por defecto) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` o `error` |

Otras, para casos concretos: `SYNC_INTERVAL` (10m), `HEALTH_INTERVAL` (1m), `RESOURCES_INTERVAL` (30m), `PLEX_TIMEOUT` (20s), `MAX_STREAMS`, `PLEXMOR_KEY` (dirección del administrador), `TRAKT_CLIENT_ID` / `TRAKT_CLIENT_SECRET` (módulo de Trakt, solo VIP).

## Imagen y versiones

| Etiqueta | Canal | Para qué |
| --- | --- | --- |
| `stable` / `latest` | estable | Producción: última versión estable |
| `X.Y` / `X.Y.Z` (p. ej. `0.3.0`) | estable | Fijar una versión concreta o volver atrás |
| `beta` / `X.Y.Z-rc.N` | beta | Versiones candidatas, antes de pasar a estable |
| `main` y otras ramas | desarrollo | Probar cambios sin tocar producción |
| `sha-<commit>` | — | Una build exacta |

Imágenes para `linux/amd64` y `linux/arm64`, con procedencia y SBOM. Cada lunes se reconstruye la última versión estable con la imagen base al día, con las mismas etiquetas.

Flujo de trabajo:

1. Los cambios se hacen en una rama. Cada push publica `:<rama>`.
2. Cuando funciona, se fusiona en `main`.
3. Para publicar una versión: **Actions → Imagen Docker → Run workflow** en `main`, con el número de versión (debe tener su sección en [CHANGELOG.md](CHANGELOG.md)).
   - **Estable** (`0.3.0`): release en GitHub y `:0.3.0`, `:0.3`, `:stable` y `:latest`.
   - **Beta** (`0.3.0-rc.1`): pre-release en GitHub y `:0.3.0-rc.1` y `:beta`.

## Seguridad

- La dirección de cada usuario lleva una clave aleatoria: no la compartas. **Generar dirección nueva** en el panel invalida la anterior.
- Los tokens de Plex se guardan cifrados y nunca aparecen en el log.
- Panel con sesión firmada, protección CSRF, Content-Security-Policy estricta y sin recursos de terceros.
- Imagen distroless sin shell, usuario sin privilegios, sistema de ficheros de solo lectura y sin capacidades.
- Análisis con govulncheck, CodeQL y Trivy.

Cómo avisar de un problema de seguridad: [SECURITY.md](SECURITY.md).

## Desarrollo

```sh
go test -race ./...                      # pruebas (Plex, plex.tv y Trakt falsos de extremo a extremo)
go test -run '^$' -bench . ./...         # rendimiento de /stream
DATA_DIR=./data go run ./cmd/plexmor      # panel en http://localhost:7254
```

Solo usa la librería estándar de Go: sin dependencias externas.

## Licencia

[MIT](LICENSE): puedes usarlo, copiarlo, modificarlo y redistribuirlo libremente, también con fines comerciales, manteniendo el aviso de copyright. Las fuentes del panel (Bricolage Grotesque e Instrument Sans) tienen licencia [SIL OFL 1.1](internal/panel/static/fonts).

## Aviso legal

Este software se ofrece **tal cual**, sin garantías de ningún tipo (ver [LICENSE](LICENSE)). El autor no se hace responsable de ningún daño, pérdida ni reclamación derivados de su uso.

- **No incluye, aloja ni distribuye contenido.** Es una herramienta técnica que solo se conecta a los servidores Plex a los que cada usuario ya tiene acceso.
- **Cada usuario es el único responsable** de cómo lo usa y de cumplir la legislación aplicable (incluida la de propiedad intelectual) y las condiciones de los servicios a los que se conecta.
- **No está afiliado** a Plex, Stremio, Nuvio, AIOStreams, Trakt ni a ningún otro proyecto o marca mencionado, ni cuenta con su respaldo. Los nombres pertenecen a sus respectivos titulares.

*This software is provided "as is", without warranty of any kind. It does not include, host or distribute any content. Users are solely responsible for how they use it and for complying with applicable laws and the terms of the services they connect to. Not affiliated with or endorsed by any of the projects mentioned.*

## Apoyar el proyecto

Si te resulta útil, puedes invitarme a un café: [buymeacoffee.com/jmoruno](https://buymeacoffee.com/jmoruno) ☕
