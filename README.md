# Spotify Mix Unfcker

I'm a big fan of Spotify's generated daily and genre-specific mixes in general.
They're great to just have a nice list of music going without thinking too much what to play next, and I've discovered plenty of great new-to-me songs through them.

That said, I've come to absolutely loathe their inconsistency and disruption in the list handling.

One too many times I had a list I enjoyed replaced the next day with a near-identical duplicate of another, already existing list.
And one too many times I had a horribly timed refreshing of a list mess up the whole vibe of it.
Obviously, after repeat-listening a certain song or band for the last hours, it must clearly mean I've had enough of it, so let's make sure it won't be on the refreshed list anymore.  @#%$@&!

Copying manually some songs to a dedicated playlist is an option, sure, but I don't really want to make it a chore to enjoy music.

To the rescue: [FatsAPI](https://fastapi.tiangolo.com/) and [Spotipy](https://spotipy.readthedocs.io/en/2.25.1/) to manage a dedicated playlist to copy all currently played songs.

- on startup, retrieves the playlist content via [`/playlists/{playlist_id}`](https://developer.spotify.com/documentation/web-api/reference/get-playlist) and also keep it in memory, so no need to retrieve it over and over again
- periodically retrieves the list of last played songs via [`/me/player/recently-played`](https://developer.spotify.com/documentation/web-api/reference/get-recently-played) 
- compares the recently played songs' IDs against the IDs already contained in the playlist
- creates the diff of songs, adds them to the in-memory list and calls [`/playlists/{playlist_id}/tracks`](https://developer.spotify.com/documentation/web-api/reference/add-tracks-to-playlist) to add them straight to the actual playlist
- have some web UI via FastAPI and htmx for experimenting, seeing what's going on, later to manage playlists for different genres etc, and, well, to play around with htmx


This is a first proof of concept, using just a single predefined playlist, and all code is chaotically sprinkled around in a single [`main.py`](main.py) file for now<sup>TM</sup>.

You'll need to set up your own Spotify app in their [developer portal](https://developer.spotify.com/), and create an `.env` file with the information needed, if you wanna give it a try.
