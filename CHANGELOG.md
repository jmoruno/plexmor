# Registro de cambios

Versiones con [SemVer](https://semver.org/lang/es/). Las notas de cada versión también están en las [releases](../../releases).

## [0.3.2] - 2026-11-14

### Cambiado

- Imagen base Debian 13 (distroless): certificados raíz y zona horaria al día.
- Cada semana se reconstruye la última versión estable con la imagen base actualizada, sin cambiar el código.
- CodeQL ya no analiza el código de pruebas (falsos positivos en el Plex simulado de los tests).

## [0.3.1] - 2026-11-13

### Corregido

- Los vídeos ya no se cortan a mitad detrás de Nginx o Nginx Proxy Manager. Plexmor pide que no guarden el vídeo en disco (`X-Accel-Buffering: no`): al llenarse su fichero temporal de 1 GB dejaban de leer y Plex cerraba la conexión, y el reproductor daba un error al leer el vídeo.
- Si Plex corta la conexión en mitad de un fichero, Plexmor la reanuda desde el mismo byte sin que el reproductor lo note (hasta 5 veces seguidas).
- Los cortes de Plex quedan en el log.

## [0.3.0] - 2026-11-13

### Añadido

- **Inglés:** panel y textos en Stremio en castellano o en inglés. `LANGUAGE=es|en` fija el idioma por defecto y cada usuario lo cambia en el panel; sin elección, el panel sigue el idioma del navegador.
- Nombres de idiomas de audio en inglés (`english`, `japanese`… también valen en `LANGUAGES`). Si `LANGUAGES` está vacío, sigue a `LANGUAGE`.
- Imagen en Docker Hub (`jmorunofdz/plexmor`) para amd64 y arm64, con canales `stable` y `beta`, procedencia y SBOM.
- `HEALTHCHECK` en la imagen (`plexmor health`).
- README en inglés, licencia MIT y aviso legal.
- Análisis de seguridad: govulncheck en cada push, CodeQL y Trivy.

### Seguridad

- El inicio de sesión con Plex solo puede terminarlo el navegador que lo empezó. Antes, quien adivinase el número del PIN, que plex.tv asigna en orden, podía quedarse con la sesión.
- Límite de inicios de sesión a medias.
- Content-Security-Policy con nonce: ningún script ni estilo sin firmar. Las fuentes las sirve Plexmor, sin Google Fonts.
- Cabeceras `nosniff`, `no-referrer` (las direcciones del addon no se filtran en el `Referer`), `Permissions-Policy` y HSTS detrás de HTTPS.
- Protección CSRF con Fetch Metadata, además de `Origin`, y límite de tamaño en los formularios del panel.
- CORS solo en las rutas del addon, no en el panel ni en su API.
- `/metrics` solo para administradores o con `METRICS_TOKEN`.
- La sesión nueva tras «Generar dirección nueva» también lleva `Secure`.
- La dirección del addon del administrador ya no se escribe en el log.
- Carpeta de datos con permisos `700`. El proxy de imágenes respeta los servidores desactivados. Se descarta cualquier `X-Plex-Token` del cliente, en mayúsculas o minúsculas.
- `docker-compose.yml` endurecido: `read_only`, `no-new-privileges` y `cap_drop: ALL`.

### Cambiado

- `docker-compose.yml` usa la imagen publicada en vez de construirla.
- `/{cuenta}/health` da el tipo de conexión como `local`, `remote` o `relay`.

## [0.2.0] - 2026-11-13

- Proxy de vídeo para usarlo fuera de casa, catálogos, fichas, subtítulos externos, transcodificación opcional, Trakt opcional y métricas.
- Panel web: entrar con Plex, instalar en Stremio, preferencias, bibliotecas y usuarios. Varios usuarios, cada uno con su dirección y su acceso a cada servidor.

## [0.1.0] - 2026-11-13

- Versión inicial: descubrimiento de servidores propios y compartidos, índice en memoria y fuentes de Stremio con idioma, resolución, HDR y latencia.
