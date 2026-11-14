# Installing Plexmor with Portainer

[Español](portainer.es.md) · **English**

This guide takes you from nothing to Plexmor installed in Stremio, in about ten minutes. You need:

- A machine with **Docker** and **Portainer**, on the same network as your Plex server (it can be the same machine).
- A **Plex** account with a server, your own or one shared with you.
- Optional, to use it away from home or in Stremio Web: a domain and a reverse proxy with HTTPS, such as Nginx Proxy Manager.

## 1. Pick the folders

Plexmor needs one folder of its own, and it can optionally read one folder of Plex.

**Data folder (`DATA_PATH`, required).** Where Plexmor keeps its index, the accounts and its secret. Any empty folder on the host works, for example `/opt/plexmor` or, on Unraid, `/mnt/user/appdata/plexmor`. Docker creates it if it does not exist.

**Plex config folder (`PLEX_CONFIG`, recommended).** The folder that contains Plex's `Preferences.xml`. Plexmor reads the server owner's token from it (read-only), so the owner becomes the Plexmor administrator without typing any token. Usual paths on the host:

| How Plex is installed | `PLEX_CONFIG` |
| --- | --- |
| Docker (official `plexinc/pms-docker` or `linuxserver/plex`) | `<folder you mapped to /config>/Library/Application Support/Plex Media Server` |
| Unraid | `/mnt/user/appdata/<plex folder>/Library/Application Support/Plex Media Server` |
| Linux package (.deb / .rpm) | `/var/lib/plexmediaserver/Library/Application Support/Plex Media Server` |
| Synology DSM 7 | `/volume1/PlexMediaServer/AppData/Plex Media Server` |
| Windows or macOS | No `Preferences.xml`: skip it (see below) |

Not sure? On the Plex machine run:

```sh
sudo find / -name Preferences.xml -path "*Plex Media Server*" 2>/dev/null
```

`PLEX_CONFIG` is the folder of the file it prints, without `/Preferences.xml` at the end.

**Plex on another machine, Windows or macOS?** Leave `PLEX_CONFIG` empty. The first person who signs in to the Plexmor panel becomes the administrator, so sign in yourself, with the account that owns the Plex server, right after deploying. (Advanced alternative: put the owner's token in `PLEX_TOKEN`. Plex explains how to find it in [Finding an authentication token](https://support.plex.tv/articles/204059436-finding-an-authentication-token-x-plex-token/).)

## 2. Pick the user (PUID and PGID)

Plexmor does not run as root. It must run as a user that can read `Preferences.xml` and write to the data folder. Use the same user as Plex. On the Plex machine:

```sh
ls -ln "<PLEX_CONFIG>/Preferences.xml"
```

The two numbers after the permissions are the user and the group, for example `1000 1000`. Those are your `PUID` and `PGID`. On Unraid they are `99` and `100`, which is the default.

If you skipped `PLEX_CONFIG`, use `1000` and `1000`, or any user you like.

## 3. Create the stack in Portainer

1. In Portainer, open your environment and go to **Stacks → Add stack**.
2. **Name:** `plexmor`.
3. **Build method: Web editor.** Paste the contents of [`docker-compose.yml`](../docker-compose.yml) as it is. You do not need to edit it.
4. Scroll down to **Environment variables** and switch to **Advanced mode**. Paste your values, for example:

   ```env
   DATA_PATH=/opt/plexmor
   PLEX_CONFIG=/opt/plex/config/Library/Application Support/Plex Media Server
   PUID=1000
   PGID=1000
   TZ=Europe/London
   LANGUAGE=en
   ```

   Leave out what you do not use: without `PLEX_CONFIG`, just remove that line. Spaces in paths are fine; do not add quotes.
5. Click **Deploy the stack**.

Instead of the Web editor you can choose **Repository**, with URL `https://github.com/jmoruno/plexmor`, reference `refs/heads/main` and compose path `docker-compose.yml`. Portainer then takes the compose file from GitHub.

## 4. Check that it started

In **Containers**:

- `plexmor` should be **running** and, after a few seconds, **healthy**.
- `plexmor-init` shows as **exited**. That is expected: it only prepares the data folder and stops.

Open the `plexmor` logs. You should see `plexmor listening`, then `servers refreshed` and `server indexed` for each Plex server. Indexing the first time takes from seconds to a few minutes, depending on the size of your libraries.

## 5. Sign in and install in Stremio

1. Open `http://<server-ip>:7254/` in your browser.
2. Press **Sign in with Plex** and sign in in the window that opens. The first account becomes the administrator.
3. Press **Install in the app** to open Stremio, or **Open in Stremio Web**. You can also copy the address of the addon and paste it in Stremio, Nuvio or AIOStreams.

That address is personal: it carries your key. Do not share it. If it leaks, **Generate a new address** in the panel invalidates the old one.

Anyone you share your Plex with can sign in to the same panel and gets their own address and settings.

## 6. Use it away from home (optional)

Stremio outside your network, and Stremio Web anywhere, need Plexmor over HTTPS:

1. In your reverse proxy, create a host such as `plexmor.example.com` that forwards to `http://<server-ip>:7254`, with an SSL certificate. In Nginx Proxy Manager: **Hosts → Proxy Hosts → Add**, scheme `http`, port `7254`, and in the **SSL** tab request a certificate with **Force SSL** on.
2. Add `PUBLIC_URL=https://plexmor.example.com` to the stack variables and click **Update the stack**.
3. Open the panel through `https://plexmor.example.com` and install the addon from there.

The default playback mode, **Automatic**, sends the video through Plexmor only when the Plex server cannot be reached from outside. Your Plex token never leaves your network.

## Updating

**Stacks → plexmor → Update the stack**, with **Re-pull image and redeploy** on. Your data and accounts stay in `DATA_PATH`.

## Troubleshooting

| What you see | What to do |
| --- | --- |
| `permission denied` on `/data` or on `Preferences.xml` | `PUID`/`PGID` do not match the owner of the files: check step 2 |
| `PlexOnlineToken not found` | The Plex server is not signed in to a Plex account: sign it in under Plex settings, or leave `PLEX_CONFIG` empty |
| The panel says there are no servers yet | Wait a minute after signing in and reload: Plexmor is discovering and indexing them |
| A server shows **Offline right now** | Plexmor cannot reach it. Check that Plex is running and that "Remote Access" or the local network address works |
| Stremio Web does not install the addon | Stremio Web only accepts HTTPS addresses: see step 6 |
| AIOStreams times out on the `https://` address | If AIOStreams runs behind a VPN (for example Gluetun) it may not reach your own domain. Use `http://<server-ip>:7254/<key>/manifest.json` there; videos still use `PUBLIC_URL` |

Still stuck? Open an [issue](https://github.com/jmoruno/plexmor/issues) with the `plexmor` logs (they never contain tokens or addon addresses).
