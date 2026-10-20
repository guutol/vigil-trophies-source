# Connecting your launcher to Vigil

Hey! I put this together to show how your launcher could send trophies straight to Vigil, so people wouldn't need the separate Vigil app for this integration.

The linking flow and the endpoints below already exist. The launcher integration still needs to be built. I'd start with **new unlocks**, then work on importing old progress separately.

## The basic idea

1. The player clicks **Connect to Vigil** in your launcher.
2. Their browser opens Vigil, where they sign in and approve the connection.
3. Your launcher gets a token for that player's linked device.
4. When the game unlocks a trophy, your launcher sends its ID to Vigil.
5. Vigil saves it and updates the player's profile and eligible ranking stats.

Your side handles detecting unlocks and keeping an offline queue. My side handles accounts, the trophy catalog, storing progress and the website. Plain HTTPS and JSON are enough; you don't need a particular language or SDK.

**Production base URL:** `https://vigiltrophies.com`

The examples use made-up codes, tokens and event IDs. Don't send sample unlocks to production. We'll use a separate test setup first.

## 1. Connect the player

Start the connection with an empty POST:

```http
POST /api/device/authorize
```

Example `200` response:

```json
{
  "device_code": "EXAMPLE_PRIVATE_DEVICE_CODE",
  "user_code": "ABCD-EFGH",
  "verification_uri": "https://vigiltrophies.com/link",
  "verification_uri_complete": "https://vigiltrophies.com/link?code=ABCD-EFGH",
  "expires_in": 600,
  "interval": 5
}
```

Open the returned `verification_uri_complete` in the player's browser. Also show `user_code` so they can enter it manually if needed. The player approves there and gives the connection a device name. Your launcher never needs their password or browser cookies.

Keep `device_code` private. Poll using the interval returned by the server:

```http
POST /api/device/token
Content-Type: application/json
```

```json
{ "device_code": "EXAMPLE_PRIVATE_DEVICE_CODE" }
```

While waiting, the server returns `400` with an error code:

| `error` | What to do |
| --- | --- |
| `authorization_pending` | Keep waiting; respect `interval`. |
| `slow_down` | Increase the delay between polls. |
| `access_denied` | Stop; the player declined. |
| `expired_token` | Stop and offer to start again. |
| `invalid_grant` | The code is invalid or already used; start again if the player wants to. |

After approval, you get `200`:

```json
{ "access_token": "vgl_EXAMPLE_TOKEN", "token_type": "Bearer" }
```

Save the token securely before moving on. It is returned only once. There's no refresh-token flow or scheduled token expiry in the current API; a revoked or invalid token needs a new connection.

Optional: `GET /api/agent/me` with the Bearer token returns `username`, `device_name`, `profile_url` and `site_url`. You can use this to show **Connected as ...** and an **Open profile** button.

## 2. Send an unlock

For Bloodborne, the existing trophy set is `NPWR05818_00`. Use the actual numeric trophy IDs, not their position in a translated list. For other games, we'll confirm the catalog and ID mapping first.

```http
POST /api/agent/unlocks
Content-Type: application/json
Authorization: Bearer vgl_EXAMPLE_TOKEN
```

```json
{
  "events": [
    {
      "event_id": "5c166886-7569-4c6e-9605-7ccf5a08a119",
      "source": "bbport",
      "trophy_set_id": "NPWR05818_00",
      "trophy_id": 1
    }
  ]
}
```

- Generate a UUID for `event_id` **once**, save it with the event, and reuse it on retries.
- `source: "bbport"` is already accepted for the Bloodborne port. New source names need to be agreed and added on my side first.
- `unlocked_at` is optional. If you know the actual unlock time, send an ISO 8601 timestamp with a timezone, preferably UTC. Otherwise leave it out; don't invent an old date.
- Don't send a username or user ID. The token selects the account.
- **Don't send the native platinum trophy itself** — Vigil calculates it from the base-game trophies. Sending its ID returns `platinum_not_allowed`.

Example `200` response:

```json
{ "accepted": 1, "duplicates": 0, "pending": 0, "rejected": [] }
```

`accepted` means saved, not necessarily eligible for the ranking. `duplicates` means already recorded, so don't send it again. `pending` means stored while its catalog is missing; it isn't ready to display as a known trophy yet. `rejected` contains `{ "event_id": "...", "reason": "..." }` for events that need attention.

The API accepts 1–100 events per request and can return `200` with some events rejected. Check the body, not just the HTTP status. For live unlocks, one event per request makes confirmation and popups simpler. Each account can earn a trophy once per trophy set, even across different devices or event IDs.

## 3. Offline, retries and notifications

Save events to disk before sending them. Keep them across restarts and retry timeouts, connection failures and `5xx` responses with increasing delays. On `429`, respect `Retry-After`. On `401`, stop sending and ask the player to reconnect. Other `4xx` responses need handling rather than endless retries. Error bodies use `{ "error": "...", "message": "..." }`; use `error` for logic, since messages may be Portuguese.

Remove acknowledged events from the outgoing queue. Keep rejected events separately with their reason so they can be diagnosed. A lost response is safe to retry with the same event ID.

The API doesn't show desktop notifications by itself. If your launcher handles them, show one popup for a confirmed new unlock and none for duplicates or imported history. We should also avoid duplicate popups if the Vigil app is running at the same time. The unlock response doesn't list newly computed platinums, so a launcher-side platinum popup still needs an agreed way to detect and announce it once.

For unlocked trophy details, including hidden names and artwork:

```http
GET /api/agent/trophies/NPWR05818_00/1?lang=en
Authorization: Bearer vgl_EXAMPLE_TOKEN
```

This returns the trophy's `name`, `description`, `grade`, `game` and `icon_url`, plus identifiers. It only works after that account's unlock is stored. `404` can mean not unlocked or unknown; the public catalog intentionally hides locked secrets. `lang=pt-BR` is also supported, with English fallback when needed. A failed metadata request shouldn't discard the saved unlock.

## A few things to agree before shipping

- **Old progress:** there's no server-side `historical` flag or dedicated bulk-history endpoint yet. Old progress goes through the same checks as live events, and a large import can be flagged. Long offline backlogs have the same limitation. Let's agree on a proper import path before enabling it; don't delay fake "live" events to bypass those checks.
- **Characters:** progress belongs to the linked Vigil account, not a character or save slot. A new character doesn't award the same trophy twice. If someone switches Vigil accounts, don't send the previous account's queue to the new one.
- **Security:** store each player's token in the OS credential store where possible. Don't log it, bundle it in a release or use a shared secret for everyone. The current token isn't restricted to one game, so it needs to be treated carefully. Players can revoke the connection at [Linked devices](https://vigiltrophies.com/settings/devices). A launcher-side disconnect should stop sync and clear its local token; server-side revocation currently happens on the website.
- **Trust:** local game data can be edited. Direct integration doesn't make it cheat-proof. Vigil stores server receipt times, keeps usable client timestamps and checks some suspicious patterns; that isn't proof of a legitimate playthrough or a separate character's progress.
- **Testing:** there's no public sandbox URL to share yet. I'll arrange an isolated test instance and throwaway account before we send test unlocks. We'll check linking, a real-time synthetic unlock, hidden metadata, duplicates, offline retries and revocation. Bulk history needs its own test once we agree on that flow.

For a first version, the checklist is: **Connect to Vigil → save the token → capture new unlocks → queue and retry → show the right account and connection status.** Then we can test it together and work out history and notifications before releasing it.
