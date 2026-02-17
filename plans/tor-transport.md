# Tor Transport for Holler

## Context

Holler currently exposes real IP addresses via TCP/QUIC/WebRTC. Adding `--tor` mode routes ALL traffic through Tor, hiding the peer's IP. This enables truly anonymous agent-to-agent communication. Requires an external `tor` daemon running on the machine (SOCKS5 on port 9050, control on port 9051).

## Design Decisions

### Discovery: Manual Onion Exchange (no DHT over Tor)

**Rejected**: DHT over Tor. Every DHT query is 3+ round trips through 3-hop circuits. Bootstrap alone takes 30s+, FindPeer could be minutes. DHT queries leak topology patterns to bootstrap nodes even through SOCKS5. Timing correlation attacks are trivial.

**Chosen**: Manual onion address exchange. Agents share `.onion` addresses out-of-band (over clearnet holler, or any channel) and save them to contacts. Same workflow as exchanging PeerIDs today. Maximum security — separate keys, zero metadata leakage, no discovery protocol to attack.

Security: **9/10** | Robustness: **6/10** (requires manual exchange, but agents already do this for PeerIDs)

### Onion Service: Two Ports (messaging + homepage)

One `.onion` address serves both:
- **Port 9000** → holler protocol (libp2p, agent messaging)
- **Port 80** → HTTP homepage (agent profile page, viewable in Tor Browser)

Tor natively supports multiple ports per onion service — zero extra complexity.

## Approach: Custom libp2p Transport + External Tor Daemon

We'll create a custom `transport.Transport` implementation that:
- **Dials** through Tor's SOCKS5 proxy (127.0.0.1:9050)
- **Listens** by creating a persistent v3 onion service via Tor control port (127.0.0.1:9051)
- Uses `/onion3/<addr>:<port>` multiaddr format (protocol code 445, supported by `go-multiaddr`)

### Key reference implementations researched:
- `berty/go-libp2p-tor-transport` — full libp2p transport, but tightly coupled with embedded Tor
- `cpacia/go-onion-transport` — similar, embeds Tor via go-libtor
- `cretz/bine` — Go library for Tor control protocol, creates ephemeral onion services
- `cretz/tor-dht-poc` — proof-of-concept: DHT over Tor with libp2p + bine

Our approach: **Write our own minimal transport** using `golang.org/x/net/proxy` (SOCKS5) + `github.com/cretz/bine` (control port for onion services). Keep it simple and self-contained.

## New Dependencies

```
github.com/cretz/bine          — Tor control protocol (onion service creation)
golang.org/x/net/proxy          — SOCKS5 dialing (already in go.mod as golang.org/x/net)
```

## Files to Create/Modify

### 1. `node/tor.go` (NEW) — Tor transport implementation

The `libp2p.Transport()` option uses **fx dependency injection** — it accepts a constructor function whose parameters are automatically resolved. Follow the TCP transport pattern:

```go
// Constructor — libp2p injects upgrader and rcmgr automatically
func NewTorTransport(upgrader transport.Upgrader, rcmgr network.ResourceManager) (*TorTransport, error) {
    // Connect to external Tor daemon via bine control port
    t, err := tor.Start(context.Background(), &tor.StartConf{
        NoProcessCreation: true,          // use external daemon, don't spawn
        ControlPort:       9051,
        DataDir:           "",            // not needed for external
    })
    if err != nil {
        return nil, fmt.Errorf("connect to tor control port: %w", err)
    }

    return &TorTransport{
        upgrader:    upgrader,
        rcmgr:       rcmgr,
        torCtrl:     t,
        socksAddr:   "127.0.0.1:9050",
    }, nil
}

type TorTransport struct {
    upgrader    transport.Upgrader
    rcmgr       network.ResourceManager
    socksAddr   string           // "127.0.0.1:9050"
    torCtrl     *tor.Tor         // bine Tor controller (control port connection)
}
```

Methods implementing `transport.Transport`:
- `Dial(ctx, raddr, peerID)` — extract onion host:port from `/onion3/...` multiaddr, dial via SOCKS5, then `upgrader.Upgrade(ctx, t, rawConn, network.DirOutbound, peerID, connScope)` → `CapableConn`
- `CanDial(addr)` — returns true if addr contains `/onion3/` (protocol code 445)
- `Listen(laddr)` — create persistent onion service via bine, return `TorListener`
- `Protocols()` — returns `[]int{445}` (ma.P_ONION3)
- `Proxy()` — returns `true`

### 2. `node/tor_listener.go` (NEW) — Tor listener wrapping onion service

Wraps bine's `OnionService` as a `transport.Listener`. The flow is:
1. `bine.Listen()` → creates v3 onion service with persistent key, returns `OnionService` with embedded `net.Listener`
2. Raw TCP accept → `upgrader.Upgrade(ctx, t, rawConn, network.DirInbound, "", connScope)` → `CapableConn`

```go
type TorListener struct {
    onion     *tor.OnionService  // bine onion service (has Accept())
    transport *TorTransport
    upgrader  transport.Upgrader
    maddr     ma.Multiaddr       // /onion3/<base32addr>:<port>
}
```

Methods:
- `Accept()` — `onion.Accept()` returns raw net.Conn, then upgrade via upgrader
- `Close()` — tear down onion service
- `Addr()` — returns net.Addr
- `Multiaddr()` — returns `/onion3/<base32-addr>:<port>`

Onion service creation (two ports — messaging + HTTP):
```go
onion, err := torCtrl.Listen(ctx, &tor.ListenConf{
    Version3:    true,
    RemotePorts: []int{9000, 80},    // 9000=holler protocol, 80=HTTP homepage
    Key:         loadedOnionKey,     // nil = generate new, or load from tor_key
})
// onion.ID = "abc123...xyz" (56-char base32, without .onion)
// onion.Key = ed25519 private key (save for persistence)
```

### 3. `node/tor_check.go` (NEW) — Pre-flight checks

```go
func CheckTorAvailable() error
```

- Verify SOCKS5 proxy at 127.0.0.1:9050 (TCP connect test)
- Verify control port at 127.0.0.1:9051 (TCP connect test)
- Return actionable errors:
  - macOS: `"Tor not running. Install: brew install tor && brew services start tor"`
  - Linux: `"Tor not running. Install: sudo apt install tor && sudo systemctl start tor"`
  - Also remind: `"Ensure 'CookieAuthentication 1' or 'HashedControlPassword ...' is set in /etc/tor/torrc"`

### 4. `node/tor_key.go` (NEW) — Onion key persistence

```go
func LoadOrCreateOnionKey(hollerDir string) (crypto.PrivateKey, error)
func SaveOnionKey(hollerDir string, key crypto.PrivateKey) error
```

- Key file: `~/.holler/tor_key` (0600 permissions)
- **Separate from libp2p key** — onion identity and holler identity are independent (key isolation)
- On first run: bine generates a new Ed25519 key → save to `tor_key`
- On subsequent runs: load from `tor_key` → pass to `ListenConf.Key`
- Stable `.onion` address across restarts — agents can save it permanently

### 5. `node/tor_homepage.go` (NEW) — Agent profile HTTP server

Serves an agent profile page on port 80 of the onion service. Viewable in Tor Browser.

```go
func StartHomepage(ctx context.Context, listener net.Listener, peerID string, onionAddr string) error
```

- Simple HTTP handler on the onion service's port 80 listener
- Serves a static HTML page with:
  - Agent name (from config or flag)
  - PeerID
  - Onion address
  - Holler version
  - Public key fingerprint
  - Optional: custom bio/description from `~/.holler/profile.json`
- Minimal HTML, no JS, no external resources (Tor Browser safe)
- Response headers: no caching, no cookies, no tracking

### 6. `node/host.go` (MODIFY) — Add Tor mode to NewHost

New function `NewHostTor()` — stripped-down host with only TorTransport, **NO DHT**:

```go
func NewHostTor(ctx context.Context, privKey crypto.PrivKey) (host.Host, error) {
    cm, _ := connmgr.NewConnManager(10, 100)

    h, err := libp2p.New(
        libp2p.Identity(privKey),
        libp2p.Transport(NewTorTransport),   // fx injects upgrader+rcmgr
        libp2p.ListenAddrStrings(
            "/onion3/0000...0000:9000",      // placeholder — TorTransport.Listen() replaces with real addr
        ),
        libp2p.DisableRelay(),               // no relay over Tor
        libp2p.ConnectionManager(cm),
        // NO: DHT, AutoRelay, NATPortMap, HolePunching, AutoNAT — none needed over Tor
    )
    return h, err
}
```

Note: no `dhtPtr` parameter — Tor mode doesn't use DHT at all.

### 7. `identity/contacts.go` (MODIFY) — Extend contacts with onion addresses

Current format: `map[string]string` (alias → PeerID)

New format with backward compatibility:
```go
// ContactEntry holds a peer's identity and optional onion address.
type ContactEntry struct {
    PeerID string `json:"peer_id"`
    Onion  string `json:"onion,omitempty"`  // e.g. "abc...xyz.onion:9000"
}

// Contacts maps alias names to contact entries.
// Backward compat: if JSON value is a plain string, treat as PeerID-only.
type Contacts map[string]ContactEntry
```

Custom `UnmarshalJSON` on Contacts to handle both old format (`"vrgo": "12D3KooW..."`) and new format (`"vrgo": {"peer_id": "12D3KooW...", "onion": "abc...xyz.onion:9000"}`).

New CLI:
```bash
holler contacts add vrgo 12D3KooW... --onion abc...xyz.onion:9000
holler contacts list   # shows onion column when present
```

### 8. `cmd/root.go` (MODIFY) — Add `--tor` as persistent flag

```go
var TorMode bool

func init() {
    rootCmd.PersistentFlags().BoolVar(&TorMode, "tor", false, "Route all traffic through Tor (requires tor daemon)")
}
```

All subcommands read `TorMode` to decide between `NewHost()` and `NewHostTor()`.

### 9. `cmd/listen.go` (MODIFY) — Tor-aware listener

When `TorMode`:
1. `CheckTorAvailable()` — fail fast with helpful error
2. `LoadOrCreateOnionKey()` — persistent .onion address
3. `NewHostTor()` instead of `NewHost()` — no DHT
4. `StartHomepage()` — serve agent profile on port 80
5. Print onion address: `Listening as <peerID> via Tor: <addr>.onion:9000`
6. Print homepage URL: `Homepage: http://<addr>.onion`
7. **No DHT bootstrap** — no discovery, direct connections only
8. **Outbox retry uses Tor** — `retryOutboxLoop` inherits the Tor host, so all outbox deliveries go through SOCKS5 automatically

### 10. `cmd/send.go` (MODIFY) — Tor-aware sender

When `TorMode`:
1. `CheckTorAvailable()`
2. `NewHostTor()` — no DHT
3. Resolve contact → get onion address from `contacts.json`
4. Dial `/onion3/<addr>:9000` directly — no DHT lookup
5. If contact has no onion address: `"no onion address for <alias> — add with: holler contacts add <alias> <peerID> --onion <addr>"`

### 11. `cmd/ping.go` (MODIFY) — Same pattern as send

## Connection Flow

### Sending (Dial) — NO DHT
```
holler send --tor vrgo "hello"
  → CheckTorAvailable() — verify SOCKS5 + control port
  → contacts.json: vrgo → {peer_id: "12D3KooW...", onion: "abc...xyz.onion:9000"}
  → NewHostTor() — libp2p host with TorTransport only, NO DHT
  → TorTransport.Dial():
      → SOCKS5 connect to abc...xyz.onion:9000
      → upgrader.Upgrade() handles Noise + yamux handshake
  → Send message over upgraded stream
```

### Receiving (Listen) — NO DHT
```
holler listen --tor
  → CheckTorAvailable() — verify SOCKS5 + control port
  → LoadOrCreateOnionKey() — persistent .onion address
  → NewHostTor() — creates onion service via bine control port
  → StartHomepage() — HTTP profile on port 80
  → Print: "Listening via Tor: abc...xyz.onion:9000"
  → Print: "Homepage: http://abc...xyz.onion"
  → Accept loop:
      → bine onion service accepts raw TCP on port 9000
      → upgrader.Upgrade() handles Noise + yamux
      → RegisterHandler processes message
  → Outbox retry also goes through Tor (same host)
```

### Onion Address Exchange (one-time setup)
```
# Agent A starts Tor listener, gets onion address
holler listen --tor
# → "Listening via Tor: abc...xyz.onion:9000"

# Agent A tells Agent B their onion address (over clearnet holler, any channel)
holler send vrgo "my tor address: abc...xyz.onion:9000"

# Agent B saves it
holler contacts add hoot 12D3KooWJCFH... --onion abc...xyz.onion:9000

# Now Agent B can message Agent A over Tor
holler send --tor hoot "hello via tor"
```

## Mixed Network: Tor ↔ Clearnet

**Tor-only peers can only talk to other Tor peers.** This is by design — if we allowed Tor→clearnet dialing, it would leak information through exit nodes and defeat the purpose.

The network naturally partitions:
- **Clearnet peers** use DHT discovery, TCP/QUIC direct connections
- **Tor peers** use manual onion exchange, SOCKS5 connections through Tor
- **A peer can run both modes** simultaneously (two separate `holler listen` processes with the same `--dir`) to bridge the two networks — advanced use case

When `--tor` is set and the contact has no onion address, send fails with a clear error rather than falling back to clearnet.

## Security Model

1. **No IP leak**: When `--tor`, only TorTransport is registered. `CanDial()` rejects non-onion addrs. No TCP/QUIC/WebRTC. All outbound go through SOCKS5.

2. **No DHT**: Zero discovery protocol traffic. No query patterns to analyze. No timing correlation from DHT chatter. Contacts are resolved locally from `contacts.json`.

3. **Key isolation**: Onion service key (`tor_key`) is completely separate from libp2p identity key (`key.bin`). Compromising one does not compromise the other. Revoking an onion address doesn't affect your PeerID.

4. **Onion service key persistence**: `~/.holler/tor_key` (Ed25519, 0600). Stable `.onion` address across restarts.

5. **Control port auth**: bine supports both cookie auth and hashed password auth:
   - Cookie (default on most installs): `CookieAuthentication 1` in torrc
   - Password: `HashedControlPassword <hash>` in torrc + env var `HOLLER_TOR_CONTROL_PASSWORD`

6. **Outbox retry safety**: `retryOutboxLoop` uses the same `h host.Host` created with `NewHostTor()`. All connections go through TorTransport — no IP leak on retry.

7. **No DNS leak**: TorTransport only dials onion addresses (resolved by Tor internally). No DNS resolution happens outside Tor.

8. **Homepage safety**: HTTP served on port 80 of onion service. No JS, no external resources, no cookies. Safe for Tor Browser visitors.

## Data Directory (updated)

```
~/.holler/
  key.bin         Ed25519 private key for libp2p identity (0600)
  tor_key         Ed25519 private key for .onion address (0600, created on first --tor run)
  profile.json    Optional agent profile for homepage (name, bio)
  contacts.json   alias → {peer_id, onion} map
  inbox.jsonl     received messages
  sent.jsonl      sent message history
  outbox.jsonl    pending messages awaiting delivery
```

## Verification

1. **Build**: `go build ./...` — verify compilation

2. **Pre-flight check** (no tor running):
   ```bash
   holler listen --tor
   # Error: Tor not running. Install: brew install tor && brew services start tor
   ```

3. **Onion service creation**:
   ```bash
   brew install tor && brew services start tor
   holler listen --tor -v
   # [debug] Tor control port connected
   # [debug] Onion service created: abc...xyz.onion
   # [debug] Homepage serving on port 80
   # Listening as 12D3KooW... via Tor: abc...xyz.onion:9000
   # Homepage: http://abc...xyz.onion
   # Verify: no real IP in any output
   ```

4. **Homepage in Tor Browser**:
   ```
   Open Tor Browser → http://abc...xyz.onion
   → Agent profile page with PeerID, onion address, version
   ```

5. **Direct Tor send** (two terminals, same machine for testing):
   ```bash
   # Terminal 1:
   holler --dir /tmp/agent-a listen --tor -v
   # → prints onion address, e.g. abc...xyz.onion:9000

   # Terminal 2: add contact with onion address, then send
   holler --dir /tmp/agent-b contacts add agent-a <peerID> --onion abc...xyz.onion:9000
   holler --dir /tmp/agent-b send --tor agent-a "test via tor"
   # → message delivered via Tor
   ```

6. **Key persistence**:
   ```bash
   holler listen --tor -v  # note onion address
   # Ctrl+C
   holler listen --tor -v  # same onion address (loaded from ~/.holler/tor_key)
   ```

7. **No-onion error**:
   ```bash
   holler contacts add bob 12D3KooW...   # no --onion
   holler send --tor bob "hello"
   # Error: no onion address for bob — add with: holler contacts add bob <peerID> --onion <addr>
   ```

8. **Backwards compat** — non-Tor peers unaffected:
   ```bash
   holler send feesh9 "normal message"  # works as before, no Tor involved
   ```

## Version Bump

Bump to v0.3.0 in `cmd/version.go` — Tor transport is a major new feature.
