# Sidebar preferences

Pinned chats belong to the authenticated account on a Spool server. Desktop,
Web/PWA, and Android use the same server record. Different accounts and different
servers have independent pins. The collapsed state of Pinned, Projects, and
Recents stays on each device, as do individual project expansion, pagination, and
scroll position. Pinned chats also remain in Recents.

The server stores JSON in its existing SQLite `settings` table under
`sidebar:user:<user ID>`. The record contains ordered `pinned` chat IDs and a
monotonically increasing `revision`. Pinning puts the chat first and expands
Pinned on the device performing the action. Only chats the user
can read can be newly pinned; clients join the saved IDs against authorized
navigation records before rendering.

`GET /api/navigation` and the WebSocket `navigation` event include a `sidebar`
snapshot. Updates use the authenticated, CSRF-protected
`POST /api/sidebar-preferences` endpoint with one operation:

```json
{"chat_id":"chat-id","pinned":true}
```

The server applies changes atomically to the latest record, increments its
revision, and invalidates navigation. Repeating a pin leaves its position intact.
Connected devices receive their own
authorized snapshot through the existing workspace stream. When the stream is
unavailable, the existing navigation refresh restores synchronization. Clients
serialize their own writes, show changes optimistically, rebase pending operations
over incoming snapshots, and reject older revisions. Failed writes roll back
their operation and display the existing error UI.

Web and Desktop retain a localStorage cache at
`<app base path>spool.sidebar.v1:<encoded user ID>`; Android uses the private
`sidebar` SharedPreferences file with the JSON key `[server profile ID,user ID]`.
These caches contain pins for initial rendering and each device's collapsed
section booleans. Server data restores pins even after clearing client storage
or changing the Desktop loopback port. Section state remains tied to that browser
origin or Android server/account cache; it does not synchronize.

For existing installations, the first device with locally pinned chats
can send `{"initial":{"pinned":[...]}}` when the server
revision is zero. The server imports accessible pins once. An existing server
record always wins, so another device's older cache cannot overwrite it.
LocalStorage belonging to an old, inaccessible Desktop origin cannot be migrated
automatically. The synchronized state becomes durable when the server accepts
the update; device caches do not constitute a durable offline operation queue.
