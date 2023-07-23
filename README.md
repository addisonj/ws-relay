# WS-Relay

This repo contains a small proof-of-concept of a small service for relaying the body of HTTP requests to websocket sessions.

## Explanation

Suppose a user wants to temporarily tail a stream of data (logs, events, etc) produced by a service and do this via a web browser session. Generally, these services won't be accessible by a user's browser.

Instead, this relay allows for the following:

1. The user creates a new relay session by doing a `POST /session` to this service and receives back a token which identifies the session
2. The user starts a websocket connection to `/session/receive/:token`
3. The service (or a sidecar/daemon) is configured to write the data to `/session/send/:token` as a basic post request
4. Any post bodies are relayed to the websocket session


## Running

```
# in one terminal
go build
./ws-relay

# in second terminal
curl -X POST localhost:8080/session # copy the token from the response

# in third terminal
websocat ws://localhost:8080/session/receive/<token>

# in second terminal
curl -X POST localhost:8080/session/send/<token> -d 'arbitrary data'

# see the data from the post in the websocat command
```


## Considerations

* All of this is done in memory, which isn't a problem for a single instance, but we may prefer multiple instances. We either then need to use session affinity (based on the token) or some other mechanism to route the data to the correct host. Socket.io or redis being potential options
* Security - none is implemented here :)
* Ideally, we could use HTTP2 streams for the send by the service so that we don't need to make lots of requests and just keep shoving data down the same socket

