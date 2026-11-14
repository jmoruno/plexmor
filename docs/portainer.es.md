# Instalar Plexmor con Portainer

**Español** · [English](portainer.md)

Esta guía te lleva de cero a tener Plexmor instalado en Stremio, en unos diez minutos. Necesitas:

- Una máquina con **Docker** y **Portainer**, en la misma red que tu servidor Plex (puede ser la misma máquina).
- Una cuenta de **Plex** con un servidor, tuyo o compartido contigo.
- Opcional, para usarlo fuera de casa o en Stremio Web: un dominio y un proxy inverso con HTTPS, como Nginx Proxy Manager.

## 1. Elige las carpetas

Plexmor necesita una carpeta propia y, si quieres, puede leer una carpeta de Plex.

**Carpeta de datos (`DATA_PATH`, obligatoria).** Donde Plexmor guarda el índice, las cuentas y su secreto. Vale cualquier carpeta vacía del host, por ejemplo `/opt/plexmor` o, en Unraid, `/mnt/user/appdata/plexmor`. Docker la crea si no existe.

**Carpeta de configuración de Plex (`PLEX_CONFIG`, recomendada).** La carpeta que contiene el `Preferences.xml` de Plex. Plexmor lee de ahí el token del dueño del servidor (solo lectura), así que el dueño queda como administrador de Plexmor sin escribir ningún token. Rutas habituales en el host:

| Cómo está instalado Plex | `PLEX_CONFIG` |
| --- | --- |
| Docker (oficial `plexinc/pms-docker` o `linuxserver/plex`) | `<carpeta que montaste en /config>/Library/Application Support/Plex Media Server` |
| Unraid | `/mnt/user/appdata/<carpeta de Plex>/Library/Application Support/Plex Media Server` |
| Paquete de Linux (.deb / .rpm) | `/var/lib/plexmediaserver/Library/Application Support/Plex Media Server` |
| Synology DSM 7 | `/volume1/PlexMediaServer/AppData/Plex Media Server` |
| Windows o macOS | No hay `Preferences.xml`: sáltatelo (ver abajo) |

¿No lo tienes claro? En la máquina de Plex ejecuta:

```sh
sudo find / -name Preferences.xml -path "*Plex Media Server*" 2>/dev/null
```

`PLEX_CONFIG` es la carpeta del fichero que te salga, sin el `/Preferences.xml` del final.

**¿Plex en otra máquina, en Windows o en macOS?** Deja `PLEX_CONFIG` vacío. La primera persona que entre en el panel de Plexmor queda como administradora, así que entra tú, con la cuenta dueña del servidor Plex, nada más desplegarlo. (Alternativa avanzada: pon el token del dueño en `PLEX_TOKEN`. Plex explica cómo encontrarlo en [Finding an authentication token](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/).)

## 2. Elige el usuario (PUID y PGID)

Plexmor no se ejecuta como root. Tiene que correr con un usuario que pueda leer `Preferences.xml` y escribir en la carpeta de datos. Usa el mismo que Plex. En la máquina de Plex:

```sh
ls -ln "<PLEX_CONFIG>/Preferences.xml"
```

Los dos números que salen después de los permisos son el usuario y el grupo, por ejemplo `1000 1000`. Esos son tu `PUID` y tu `PGID`. En Unraid son `99` y `100`, que es el valor por defecto.

Si te has saltado `PLEX_CONFIG`, usa `1000` y `1000`, o el usuario que quieras.

## 3. Crea el stack en Portainer

1. En Portainer, entra en tu entorno y ve a **Stacks → Add stack**.
2. **Name:** `plexmor`.
3. **Build method: Web editor.** Pega el contenido de [`docker-compose.yml`](../docker-compose.yml) tal cual. No hace falta tocarlo.
4. Baja hasta **Environment variables** y cambia a **Advanced mode**. Pega tus valores, por ejemplo:

   ```env
   DATA_PATH=/opt/plexmor
   PLEX_CONFIG=/opt/plex/config/Library/Application Support/Plex Media Server
   PUID=1000
   PGID=1000
   TZ=Europe/Madrid
   LANGUAGE=es
   ```

   Quita lo que no uses: sin `PLEX_CONFIG`, borra esa línea. Los espacios en las rutas no dan problemas; no pongas comillas.
5. Pulsa **Deploy the stack**.

En lugar de Web editor puedes elegir **Repository**, con URL `https://github.com/jmoruno/plexmor`, referencia `refs/heads/main` y ruta del compose `docker-compose.yml`. Así Portainer coge el compose de GitHub.

## 4. Comprueba que ha arrancado

En **Containers**:

- `plexmor` debe estar **running** y, a los pocos segundos, **healthy**.
- `plexmor-init` aparece como **exited**. Es lo normal: solo prepara la carpeta de datos y se para.

Abre los logs de `plexmor`. Debe salir `plexmor listening`, y después `servers refreshed` y `server indexed` por cada servidor Plex. La primera indexación tarda de segundos a unos minutos, según lo grandes que sean tus bibliotecas.

## 5. Entra e instala en Stremio

1. Abre `http://<ip-del-servidor>:7254/` en el navegador.
2. Pulsa **Entrar con Plex** e inicia sesión en la ventana que se abre. La primera cuenta queda como administradora.
3. Pulsa **Instalar en la app** para abrir Stremio, o **Abrir en Stremio Web**. También puedes copiar la dirección del addon y pegarla en Stremio, Nuvio o AIOStreams.

Esa dirección es personal: lleva tu clave. No la compartas. Si se filtra, **Generar dirección nueva** en el panel anula la anterior.

Cualquiera con quien compartas tu Plex puede entrar en el mismo panel, y tendrá su propia dirección y sus preferencias.

## 6. Úsalo fuera de casa (opcional)

Stremio fuera de tu red, y Stremio Web en cualquier sitio, necesitan Plexmor por HTTPS:

1. En tu proxy inverso, crea un host como `plexmor.example.com` que reenvíe a `http://<ip-del-servidor>:7254`, con certificado SSL. En Nginx Proxy Manager: **Hosts → Proxy Hosts → Add**, esquema `http`, puerto `7254`, y en la pestaña **SSL** pide un certificado con **Force SSL** activado.
2. Añade `PUBLIC_URL=https://plexmor.example.com` a las variables del stack y pulsa **Update the stack**.
3. Abre el panel por `https://plexmor.example.com` e instala el addon desde ahí.

El modo de reproducción por defecto, **Automático**, solo pasa el vídeo por Plexmor cuando el servidor Plex no es accesible desde fuera. Tu token de Plex nunca sale de tu red.

## Actualizar

**Stacks → plexmor → Update the stack**, con **Re-pull image and redeploy** activado. Tus datos y cuentas siguen en `DATA_PATH`.

## Problemas frecuentes

| Qué ves | Qué hacer |
| --- | --- |
| `permission denied` en `/data` o en `Preferences.xml` | `PUID`/`PGID` no coinciden con el dueño de los ficheros: revisa el paso 2 |
| `PlexOnlineToken not found` | El servidor Plex no tiene iniciada sesión con una cuenta de Plex: iníciala en los ajustes de Plex, o deja `PLEX_CONFIG` vacío |
| El panel dice que aún no hay servidores | Espera un minuto después de entrar y recarga: Plexmor los está descubriendo e indexando |
| Un servidor sale **Sin conexión ahora mismo** | Plexmor no llega a él. Comprueba que Plex está encendido y que funciona el acceso remoto o la dirección de la red local |
| Stremio Web no instala el addon | Stremio Web solo admite direcciones HTTPS: mira el paso 6 |
| AIOStreams da timeout con la dirección `https://` | Si AIOStreams sale por una VPN (por ejemplo Gluetun), puede no llegar a tu propio dominio. Usa ahí `http://<ip-del-servidor>:7254/<clave>/manifest.json`; los vídeos siguen usando `PUBLIC_URL` |

¿Sigues atascado? Abre un [issue](https://github.com/jmoruno/plexmor/issues) con los logs de `plexmor` (nunca contienen tokens ni direcciones del addon).
