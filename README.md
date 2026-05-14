# smuffy - the Spotify Mix Unfcker

I'm a big fan of Spotify's generated daily and genre-specific mixes in general.
They're great to just have a nice list of music going without thinking too much what to play next, and I've discovered plenty of great new-to-me songs through them.

That said, I've come to absolutely loathe their inconsistency and disruption in the list handling.

One too many times I had a list I enjoyed replaced the next day with a near-identical duplicate of another, already existing list.
And one too many times I had a horribly timed refreshing of a list mess up the whole vibe of it.
Obviously, after repeat-listening a certain song or band for the last hours, it must clearly mean I've had enough of it, so let's make sure it won't be on the refreshed list anymore.  @#%$@&!

Copying manually some songs to a dedicated playlist is an option, sure, but I don't really want to make it a chore to enjoy music.

To the rescue: smuffy!

## What it does

smuffy waits for the user to select both a target playlist (any playlist the user owns)
and a starting point (right now, a certain song in the play history, or simply the entire play history).
Once done, smuffy adds all past songs from the starting point and all future songs from here on forward
to the target playlist, preserving any generated mix in a dedicated playlist.

But it doesn't even need to be for that, got some upcoming event you want to have a memory of all the music
played there? Tadaa - smuffy!

## How it does that

smuffy itself is just running in the background, and all interaction happens through a web UI it provides.
There, you can select one of your existing playlists and start the whole process by pressing play.

On start, smuffy retrieves the selected playlist's content via [`/playlists/{playlist_id}`](https://developer.spotify.com/documentation/web-api/reference/get-playlist)
and keeps a copy of the relevant information around. It then periodically

- retrieves the list of last played songs via [`/me/player/recently-played`](https://developer.spotify.com/documentation/web-api/reference/get-recently-played)
- compares the recently played songs against the ones already contained in the playlist
- collects the new songs in a list
- calls [`/playlists/{playlist_id}/tracks`](https://developer.spotify.com/documentation/web-api/reference/add-tracks-to-playlist) to add them straight to the actual playlist
- waits for a user-defined number of minutes to start over with that.

This will run for as long as you let it, i.e., until pressing stop on the web UI, which puts smuffy back in idle state,
waiting for a new start signal - maybe you changed your mood and need a new playlist to record to.

## Known Limitations

- No authentication outside the Spotify API, once you're authenticated with Spotify, anyone with access to smuffy can use it
- Spotify limits the play history to 50 songs, so you can't copy your entire life
- Spotify adds only fully played songs to the list of recently played songs, so skipped songs will be ignored (which is kinda good though)
- By design, duplicate songs aren't added to the playlist, so recording the full, raw play history as-is isn't possible (this may be an option in the future)
- There's no playlist management implemented (nor planned at this point), so creating playlists, deleting songs or other rearrangements need to be done in Spotify

## Running it

You'll need to set up your own Spotify app in their [developer portal](https://developer.spotify.com/), and create an `.env` file with the information needed, if you wanna give it a try.
Note that Spotify enforces HTTPS for callback URLs other than localhost.

### Build it
```shell
go build
```

### Run int
```shell
./smuffy
```

**NOTE: THERE IS NO USER AUTHENTICATION IN PLACE, SO YOU SHOULD NEVER RUN THIS PUBLICLY ACCESSIBLE IN ITS CURRENT STATE!**

### Use it

Go to your browser and visit http://localhost:58071

### Config it

All configuration is currently done via `.env` file which is loaded on startup.

Following entries are supported and understood. **Bold** entries are mandatory, and the tool won't work without them.

- **`SPOTIFY_ID=<string>`** to define your own Spotify app's ID
- **`SPOTIFY_SECRET=<string>`** same for the app's secret token
- **`SPOTIFY_REDIRECT_URL=<string>`** one of the app's defined callback urls, defaults to http://localhost:58071/callback
- `PLAYLIST_REQUEST_INTERVAL_MINUTES=<int>` time interval to update the playlist with the last played songs, defaults to 10 minutes
- `CACHE_AUTH_TOKEN=<int>` set to `1` if Spotify auth token should be cached in a `.cache` file to reuse it next time the tool is started, comment it out to request auth on every start

## Running it as a systemd service

To set up smuffy to run automatically in the background on startup etc., a sample systemd service file
[`smuffy.service`](smuffy.service) is provided. This expects a dedicated user `smuffy` on the system,
using `/var/lib/smuffy` as its working directory.

### Set up dedicated non-privileged user

```shell
sudo useradd --system --home-dir /var/lib/smuffy --shell /usr/sbin/nologin smuffy
sudo install -d -o smuffy -g smuffy -m 0750 /var/lib/smuffy
```

### Install smuffy

```shell
sudo install -o root -g root -m 0755 smuffy /usr/local/bin/smuffy
```

### Configure smuffy

Write a `smuffy.env` file using [`env.sample`](env.example) as base, then move it in its place.
```shell
sudo install -o smuffy -g smuffy -m 0640 smuffy.env /var/lib/smuffy/.env
```

### Set up systemd

```shell
sudo install -o root -g root -m 0644 smuffy.service /etc/systemd/system/smuffy.service
sudo systemctl daemon-reload
sudo systemctl enable --now smuffy.service
```
