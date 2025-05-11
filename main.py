import datetime
import logging
import os
import pprint
import sys
import threading
import time

from dotenv import load_dotenv
from fastapi import Depends
from fastapi import FastAPI, Request
from fastapi.exceptions import HTTPException
from fastapi.responses import RedirectResponse
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates
from spotipy import Spotify, SpotifyException
from spotipy.oauth2 import SpotifyOAuth

# Load environment variables from .env file
load_dotenv()

# Set to True to automatically redirect to Spotify auth page when needed
# Set to False to show a page with a link to the Spotify auth page instead
AUTO_REDIRECT_AUTH = False

# Keep track internally of songs added to the playlist
# Later this should be in a database
SONGS_ADDED = []

# Get environment variables
client_id = os.getenv('SPOTIPY_CLIENT_ID')
client_secret = os.getenv('SPOTIPY_CLIENT_SECRET')
redirect_uri = os.getenv('SPOTIPY_REDIRECT_URI')

# Have own username around to check if a playlist is actually ours and can/should be modified
# TODO get this from /me endpoint (and for that to work, make sure the endpoint is always called first thing)
username_uri = os.getenv('SPOTIFY_USERNAME_URI')

# Use predefined playlist for now
target_playlist_id = os.getenv("SPOTIFY_PLAYLIST_ID")

logging.basicConfig(level=logging.DEBUG)

print(os.uname())

# Verify environment variables are loaded
if all([client_id, client_secret, redirect_uri]):
    print("Environment variables loaded successfully")

else:
    print("Error: Some environment variables are missing. Please check your .env file")
    missing_vars = []
    if not client_id:
        missing_vars.append("SPOTIPY_CLIENT_ID")
    if not client_secret:
        missing_vars.append("SPOTIPY_CLIENT_SECRET")
    if not redirect_uri:
        missing_vars.append("SPOTIPY_REDIRECT_URI")
    if not username_uri:
        missing_vars.append("SPOTIFY_USERNAME_URI")
    if not target_playlist_id:
        missing_vars.append("SPOTIFY_PLAYLIST_ID")
    print(f"Missing variables: {', '.join(missing_vars)}")

    sys.exit(1)

auth_manager = SpotifyOAuth(
    client_id=client_id,
    client_secret=client_secret,
    redirect_uri=redirect_uri,
    open_browser=False,
    scope=[
        "user-library-read",
        "user-read-currently-playing",
        "user-read-recently-played",
        "playlist-read-private",
        "user-modify-playback-state",  # add to queue
        "playlist-modify-private",
    ]
)

app = FastAPI(port=58071)

templates = Jinja2Templates(directory="templates")
app.mount("/static", StaticFiles(directory="static"), name="static")


def get_spotipy_user() -> Spotify:
    if not auth_manager.validate_token(auth_manager.cache_handler.get_cached_token()):
        auth_url = auth_manager.get_authorize_url()
        print(auth_url)
        raise HTTPException(status_code=401, detail=auth_url)

    return Spotify(auth_manager=auth_manager)


def ms_to_time_str(milliseconds):
    seconds = milliseconds // 1000
    minutes = seconds // 60
    seconds = seconds % 60
    return f"{minutes:02d}:{seconds:02d}"


@app.exception_handler(401)
def unauthorized_exception_handler(request: Request, exc: HTTPException):
    if AUTO_REDIRECT_AUTH:
        return RedirectResponse(exc.detail)

    return templates.TemplateResponse(
        request=request, name="auth.html", context={"auth_url": exc.detail}
    )


@app.get("/callback")
def callback(code: str):
    auth_manager.get_access_token(code)
    return RedirectResponse("/")


@app.get("/")
def home(request: Request, spotify: Spotify = Depends(get_spotipy_user)):
    me = spotify.me()
    track = spotify.current_user_playing_track()

    if track is not None:
        playing = f"{track['item']['artists'][0]['name']} -- {track['item']['name']} ({ms_to_time_str(track['item']['duration_ms'])}){' [paused]' if not track['is_playing'] else ''}"
    else:
        playing = "Nothing"

    context = {
        "username": me['display_name'],
        "playing": playing,
        "track": track,
    }

    return templates.TemplateResponse(
        request=request, name="home.html", context=context
    )


def init_internal_list(spotify):
    global SONGS_ADDED
    SONGS_ADDED = []
    for item in spotify.playlist_items(target_playlist_id)['items']:
        SONGS_ADDED.append(item['track']['uri'])


@app.get("/foo")
def do_the_foo(request: Request, spotify: Spotify = Depends(get_spotipy_user)):
    return add_songs_to_playlist(spotify)


def add_songs_to_playlist(spotify):
    global SONGS_ADDED

    if len(SONGS_ADDED) == 0:
        init_internal_list(spotify)

    tracks = spotify.current_user_recently_played(limit=10)
    songs = []
    for track in tracks["items"]:
        songs.append(track['track']['uri'])

    # This might need a bit better handling to have the songs listed in the order they were played.
    # On the other hand, it's meant to be a random list to play shuffled anyway, so, whatever.
    diff = list(set(songs) - set(SONGS_ADDED))
    SONGS_ADDED = SONGS_ADDED + diff

    before = {
        "inlist": {"len": len(SONGS_ADDED), "uris": SONGS_ADDED},
        "played": {"len": len(songs), "uris": songs},
        "diff": diff,
    }

    if len(diff) > 0:
        spotify.playlist_add_items(target_playlist_id, diff)

    after = {
        "inlist": {"len": len(SONGS_ADDED), "uris": SONGS_ADDED},
        "played": {"len": len(songs), "uris": songs},
        "diff": list(set(songs) - set(SONGS_ADDED)),
    }
    return {"before": before, "after": after}


@app.get("/removeall")
def remove_all_songs(spotify: Spotify = Depends(get_spotipy_user)):
    spotify.playlist_remove_all_occurrences_of_items(target_playlist_id, SONGS_ADDED)
    return "OK"


@app.get("/current")
def get_current_song(spotify: Spotify = Depends(get_spotipy_user)):
    track = spotify.current_user_playing_track()
    # pprint.pprint(track)
    if track is not None:
        return f"Playing: {track['item']['artists'][0]['name']} -- {track['item']['name']} ({ms_to_time_str(track['item']['duration_ms'])}){' [paused]' if not track['is_playing'] else ''}"
    else:
        return "Not playing anything"


@app.get("/last")
def get_last_songs(request: Request, spotify: Spotify = Depends(get_spotipy_user), count: int = 10):
    songs = spotify_get_last_songs(count, spotify)

    context = {
        "count": count,
        "songs": songs,
    }

    return templates.TemplateResponse(
        request=request, name="lastsongs.html", context=context
    )


@app.get("/last-list")
def get_last_songs(request: Request, spotify: Spotify = Depends(get_spotipy_user), count: int = 10):
    songs = spotify_get_last_songs(count, spotify)

    context = {
        "count": count,
        "songs": songs,
    }

    return templates.TemplateResponse(
        request=request, name="snippets/lastsongs.html", context=context
    )


def spotify_get_last_songs(count, spotify):
    tracks = spotify.current_user_recently_played(limit=count)
    songs = []
    for idx, track in enumerate((tracks["items"])):
        artist_list = ", ".join(artists["name"] for artists in track["track"]["artists"])
        song_string = f"{idx:02}: {track['played_at']} {ms_to_time_str(track['track']['duration_ms'])}  {artist_list} -- {track['track']['name']}"
        print(song_string)
        songs.append(song_string)

    return songs


def spotify_track_to_name(track):
    artist_list = ", ".join(artists["name"] for artists in track["track"]["artists"])
    song_string = f"{artist_list} -- {track['track']['name']}  {ms_to_time_str(track['track']['duration_ms'])}"
    return song_string


templates.env.globals.update(spotify_track_to_name=spotify_track_to_name)


@app.get("/playlists")
def get_playlists(request: Request, spotify: Spotify = Depends(get_spotipy_user)):
    playlists = spotify.current_user_playlists()

    context = {
        "me": username_uri,
        "playlists": playlists["items"],
    }

    pprint.pprint(playlists)
    return templates.TemplateResponse(
        request=request, name="playlists.html", context=context
    )


@app.get("/playlist/{playlist_id}")
def get_playlist(request: Request, playlist_id: str, spotify: Spotify = Depends(get_spotipy_user)):
    try:
        playlist = spotify.playlist(playlist_id)
    except SpotifyException as e:
        raise HTTPException(status_code=e.http_status, detail=e.msg)

    tracks = spotify.playlist_items(playlist_id, limit=100)
    context = {
        "playlist": playlist,
        "tracks": tracks["items"],
    }

    pprint.pprint(context)
    return templates.TemplateResponse(
        request=request, name="playlist-songs.html", context=context
    )


@app.post("/add/{uri}")
def add_song_to_playlist(uri: str, spotify: Spotify = Depends(get_spotipy_user)):
    spotify.playlist_add_items(target_playlist_id, [uri])
    return "OK"


@app.post("/queue/{uri}")
def queue_song(uri: str, spotify: Spotify = Depends(get_spotipy_user)):
    spotify.add_to_queue(uri)
    return "OK"


DO_THREAD_STUFF = True


@app.get("/bgstart")
def bgstart():
    global DO_THREAD_STUFF

    DO_THREAD_STUFF = True
    return "Background processing started"


@app.get("/bgstop")
def bgstart():
    global DO_THREAD_STUFF

    DO_THREAD_STUFF = False
    return "Background processing stopped"


def bgprocess():
    next_call = time.time()
    while True:
        if DO_THREAD_STUFF:
            print(f"{datetime.datetime.now()}: {get_current_song(get_spotipy_user())}")
            add_songs_to_playlist(get_spotipy_user())
        else:
            print("not doing anything")
        next_call = next_call + (60 * 10)  # 10 minutes
        time.sleep(next_call - time.time())


timerThread = threading.Thread(target=bgprocess)
timerThread.daemon = True
timerThread.start()
