# config

A generic Go `http.Handler` that serves an embedded, Material Design web UI for
live-editing a typed configuration struct — no restart required.

Mount it on any route, point a browser at that path, and every change is
reflected in the running process immediately.

---

## Features

| | |
|---|---|
| **Generic & type-safe** | `NewConfigHandler[T]` binds directly to `*T`; no `interface{}` wrangling |
| **Zero dependencies** on the hot path | Reflection-only field traversal; `gopkg.in/yaml.v3` used only for file I/O |
| **Rich type support** | `string`, `int*`, `uint*`, `float*`, `bool`, `time.Time`, `time.Duration`, `[]T` slices, `map`, nested structs |
| **Persistence** | Optional auto-load & auto-save to a YAML or JSON file |
| **Auth** | Optional HMAC-signed cookie login page — no external session store |
| **Change hooks** | Register callbacks that receive a deep copy of the old and new config |
| **Embedded UI** | Single-file deployment; the HTML/CSS/JS is embedded via `go:embed` |

---

## Installation

```bash
go get github.com/lzh/config
```

---

## Quick Start

```go
package main

import (
    "log"
    "net/http"

    "github.com/lzh/config"
)

type ServerConfig struct {
    Host     string `config:"Bind address"`
    Port     int    `config:"Listening port"`
    Debug    bool   `config:"Enable debug logging"`
    LogLevel string `config:"Log level: debug | info | warn | error"`
}

func main() {
    cfg := &ServerConfig{Host: "0.0.0.0", Port: 8080, LogLevel: "info"}

    http.Handle("/config", config.NewConfigHandler(cfg))

    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

Open `http://localhost:8080/config` — a live form for your struct will appear.
Every save updates `*cfg` in-place; other handlers see the new values immediately.

---

## Options

Pass functional options as variadic arguments to `NewConfigHandler`.

### `WithSecret` — password-protect the UI

```go
config.NewConfigHandler(cfg,
    config.WithSecret[ServerConfig]("my-secret-key"),
)
```

Visitors are redirected to a login page and must enter the correct key before
viewing or editing the configuration. Auth state is stored in an `HttpOnly`
cookie signed with HMAC-SHA256. The session lasts 7 days.

### `WithFile` — persist config to disk

```go
config.NewConfigHandler(cfg,
    config.WithFile[ServerConfig]("config.yaml"),  // or .json
)
```

- The file is **loaded on startup** (silently ignored if it does not yet exist).
- The file is **written after every successful save** from the UI.
- Format is inferred from the extension: `.yaml` / `.yml` → YAML; anything else → JSON (indented).

### `WithHook` — react to changes

```go
config.NewConfigHandler(cfg,
    config.WithHook(func(old, new ServerConfig) {
        log.Printf("config changed: %+v → %+v", old, new)
        // reconnect database, reload rate-limiter, etc.
    }),
)
```

- Multiple hooks can be registered; they are called **in registration order**.
- `old` is a **deep copy** (JSON round-trip) of the config taken before the form
  was applied, so comparing `old` and `new` is always safe.
- Hooks are only called when the save succeeds (including file persistence, if
  configured).

### Combining options

```go
http.Handle("/admin/config", config.NewConfigHandler(cfg,
    config.WithSecret[AppConfig]("s3cr3t"),
    config.WithFile[AppConfig]("config.yaml"),
    config.WithHook(func(old, new AppConfig) {
        applyRateLimit(new.RateLimit)
    }),
))
```

---

## Struct Tags

Use the `config` tag on exported fields to add descriptions shown in the UI.

```go
type AppConfig struct {
    Host    string        `config:"Bind address for the HTTP server"`
    Timeout time.Duration `config:"Request read/write timeout (e.g. 30s, 1m)"`
    Debug   bool          `config:"-"`  // hidden from the UI
}
```

| Tag value | Behaviour |
|---|---|
| `config:"description"` | Field is shown with the given description |
| `config:""` *(or tag absent)* | Field is shown, no description |
| `config:"-"` | Field is **hidden** from the UI |

Unexported fields are always hidden regardless of their tag.

---

## Supported Field Types

| Go type | UI control |
|---|---|
| `string` | Text input |
| `int`, `int8` … `int64` | Number input |
| `uint`, `uint8` … `uint64` | Number input |
| `float32`, `float64` | Number input |
| `bool` | Segmented button (`true` / `false`) |
| `time.Duration` | Text input with format hint (`30s`, `5m`, `1h30m`) |
| `time.Time` | `datetime-local` picker |
| `[]T` (any scalar slice) | Chip-based tag input — press Enter to add, Backspace or × to remove |
| `map[K]V` | Editable JSON textarea with inline Format button |
| nested `struct` | Editable JSON textarea with inline Format button |

---

## Running the Example

```bash
git clone https://github.com/lzh/config
cd config
go run ./example
```

Then visit:

- `http://localhost:8080/config` — config UI (secret: `demo-secret`)
- `http://localhost:8080/` — prints the current in-memory config

---

## Project Layout

```
config/
├── handler.go      # ConfigHandler, Option types, reflection helpers
├── index.html      # Embedded UI (Material Design 3, Tailwind CSS, Roboto)
├── go.mod
├── example/
│   └── main.go     # Runnable demo showing all features
└── README.md
```

---

## License

MIT
