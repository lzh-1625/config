// Package config provides a generic http.Handler that serves an embedded web UI
// for reading and live-editing a typed Go configuration struct.
//
// Mount the handler on any route with http.Handle, then point a browser at that
// path to view and update the running configuration without restarting the process.
package config

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed index.html
var htmlFile string

var tmpl = template.Must(template.New("root").Parse(htmlFile))

var (
	timeType     = reflect.TypeOf(time.Time{})
	durationType = reflect.TypeOf(time.Duration(0))
)

const (
	authCookieName = "cfg_auth"
	authCookieTTL  = 86400 * 7 // 7 days
)

// ─── Option pattern ──────────────────────────────────────────────────────────

// Option configures a ConfigHandler[T].
type Option[T any] func(*ConfigHandler[T])

// WithSecret enables a login page protected by the given secret key.
func WithSecret[T any](secret string) Option[T] {
	return func(h *ConfigHandler[T]) { h.secret = secret }
}

// WithFile enables persistence: config is loaded from path on startup and
// saved after every successful update.
// Format is inferred from extension (.yaml/.yml → YAML, else → JSON).
func WithFile[T any](path string) Option[T] {
	return func(h *ConfigHandler[T]) {
		h.filePath = path
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".yaml" || ext == ".yml" {
			h.fileFormat = "yaml"
		} else {
			h.fileFormat = "json"
		}
	}
}

// WithHook registers a callback invoked after each successful config update.
// old is a deep copy of the config before the change; new is the updated value.
// Multiple hooks are called in registration order.
func WithHook[T any](fn func(old, new T)) Option[T] {
	return func(h *ConfigHandler[T]) {
		h.hooks = append(h.hooks, fn)
	}
}

// ─── Handler ─────────────────────────────────────────────────────────────────

// ConfigHandler is an http.Handler that renders and updates a config struct via a web UI.
type ConfigHandler[T any] struct {
	config     *T
	title      string
	secret     string
	filePath   string
	fileFormat string // "json" or "yaml"
	hooks      []func(old, new T)
}

// NewConfigHandler creates a handler bound to the given config struct pointer.
// Struct fields may carry a `desc` tag for human-readable descriptions.
func NewConfigHandler[T any](cfg *T, opts ...Option[T]) *ConfigHandler[T] {
	rt := reflect.TypeOf(cfg).Elem()
	h := &ConfigHandler[T]{config: cfg, title: rt.Name()}
	for _, opt := range opts {
		opt(h)
	}
	if h.filePath != "" {
		_ = h.loadFile() // ignore "not found" on first run
	}
	return h
}

// ─── http.Handler ────────────────────────────────────────────────────────────

func (h *ConfigHandler[T]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("logout") == "1" {
		h.clearAuthCookie(w)
		http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
		return
	}

	if !h.isAuthenticated(r) {
		if r.Method == http.MethodPost {
			h.handleLogin(w, r)
		} else {
			h.renderLogin(w, r, "")
		}
		return
	}

	switch r.Method {
	case http.MethodPost:
		h.applyForm(w, r)
	default:
		h.renderPage(w, r, "")
	}
}

// ─── Auth ────────────────────────────────────────────────────────────────────

func (h *ConfigHandler[T]) authToken() string {
	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write([]byte("cfg_auth_v1"))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *ConfigHandler[T]) isAuthenticated(r *http.Request) bool {
	if h.secret == "" {
		return true
	}
	c, err := r.Cookie(authCookieName)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(c.Value), []byte(h.authToken()))
}

func (h *ConfigHandler[T]) setAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    h.authToken(),
		Path:     "/",
		MaxAge:   authCookieTTL,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *ConfigHandler[T]) clearAuthCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:   authCookieName,
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
}

func (h *ConfigHandler[T]) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderLogin(w, r, "请求错误")
		return
	}
	if r.FormValue("secret") == h.secret {
		h.setAuthCookie(w)
		http.Redirect(w, r, r.URL.Path, http.StatusSeeOther)
		return
	}
	h.renderLogin(w, r, "密钥错误，请重试")
}

// ─── File persistence ────────────────────────────────────────────────────────

func (h *ConfigHandler[T]) loadFile() error {
	data, err := os.ReadFile(h.filePath)
	if err != nil {
		return err
	}
	if h.fileFormat == "yaml" {
		return yaml.Unmarshal(data, h.config)
	}
	return json.Unmarshal(data, h.config)
}

func (h *ConfigHandler[T]) saveFile() error {
	var (
		data []byte
		err  error
	)
	if h.fileFormat == "yaml" {
		data, err = yaml.Marshal(h.config)
	} else {
		data, err = json.MarshalIndent(h.config, "", "  ")
		if err == nil {
			data = append(data, '\n')
		}
	}
	if err != nil {
		return err
	}
	return os.WriteFile(h.filePath, data, 0o644)
}

// ─── Render ───────────────────────────────────────────────────────────────────

type templateData struct {
	Title   string
	Fields  []fieldInfo
	Message string // empty | "ok" | error text
	HasAuth bool
}

type loginData struct {
	Title string
	Error string
}

func (h *ConfigHandler[T]) renderPage(w http.ResponseWriter, r *http.Request, message string) {
	data := templateData{
		Title:   h.title,
		Fields:  h.collectFields(),
		Message: message,
		HasAuth: h.secret != "",
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "config", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (h *ConfigHandler[T]) renderLogin(w http.ResponseWriter, r *http.Request, errMsg string) {
	data := loginData{Title: h.title, Error: errMsg}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "login", data); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

// ─── Apply form ───────────────────────────────────────────────────────────────

func (h *ConfigHandler[T]) applyForm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderPage(w, r, "表单解析失败: "+err.Error())
		return
	}

	// Snapshot before applying so hooks receive the old value.
	oldCfg := deepCopy(h.config)

	v := reflect.ValueOf(h.config).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		fv := v.Field(i)
		if !fv.CanSet() {
			continue
		}
		setField(fv, t.Field(i).Type, r.FormValue(t.Field(i).Name))
	}

	if h.filePath != "" {
		if err := h.saveFile(); err != nil {
			h.renderPage(w, r, "保存文件失败: "+err.Error())
			return
		}
	}

	if len(h.hooks) > 0 {
		newCfg := *h.config
		for _, hook := range h.hooks {
			hook(oldCfg, newCfg)
		}
	}

	h.renderPage(w, r, "ok")
}

// ─── Field collection ────────────────────────────────────────────────────────

// fieldInfo describes a single config field for the template.
type fieldInfo struct {
	Name      string
	Desc      string
	Category  string   // text | number | bool | slice | time | duration | json
	InputType string   // HTML input[type] value
	Value     string   // serialized value for scalar types
	Items     []string // parsed items for slice type
}

func (h *ConfigHandler[T]) collectFields() []fieldInfo {
	v := reflect.ValueOf(h.config).Elem()
	t := v.Type()
	fields := make([]fieldInfo, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fv := v.Field(i)
		if !sf.IsExported() {
			continue
		}
		if sf.Tag.Get("config") == "-" {
			continue
		}
		fields = append(fields, buildFieldInfo(sf, fv))
	}
	return fields
}

func buildFieldInfo(sf reflect.StructField, fv reflect.Value) fieldInfo {
	fi := fieldInfo{Name: sf.Name, Desc: sf.Tag.Get("config")}

	// Check concrete types before kind (Duration is int64 underneath).
	switch sf.Type {
	case timeType:
		fi.Category = "time"
		fi.InputType = "datetime-local"
		t := fv.Interface().(time.Time)
		if !t.IsZero() {
			fi.Value = t.Format("2006-01-02T15:04")
		}
		return fi
	case durationType:
		fi.Category = "duration"
		fi.InputType = "text"
		d := fv.Interface().(time.Duration)
		if d != 0 {
			fi.Value = d.String()
		}
		return fi
	}

	switch fv.Kind() {
	case reflect.Map:
		fi.Category = "json"
		if fv.IsNil() {
			fi.Value = "{}"
		} else {
			fi.Value = marshalJSON(fv.Interface())
		}
	case reflect.Struct:
		fi.Category = "json"
		fi.Value = marshalJSON(fv.Interface())
	case reflect.Slice:
		fi.Category = "slice"
		fi.Items = sliceToStrings(fv)
		fi.Value = strings.Join(fi.Items, ",")
	case reflect.Bool:
		fi.Category = "bool"
		fi.Value = strconv.FormatBool(fv.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		fi.Category = "number"
		fi.InputType = "number"
		fi.Value = strconv.FormatInt(fv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		fi.Category = "number"
		fi.InputType = "number"
		fi.Value = strconv.FormatUint(fv.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		fi.Category = "number"
		fi.InputType = "number"
		fi.Value = strconv.FormatFloat(fv.Float(), 'f', -1, 64)
	default:
		fi.Category = "text"
		fi.InputType = "text"
		fi.Value = fv.String()
	}
	return fi
}

// ─── Field set helpers ────────────────────────────────────────────────────────

// setField writes raw (form value) into v, using ft to resolve ambiguous kinds.
func setField(v reflect.Value, ft reflect.Type, raw string) {
	raw = strings.TrimSpace(raw)

	switch ft {
	case timeType:
		if raw == "" {
			v.Set(reflect.Zero(timeType))
			return
		}
		if t, err := time.ParseInLocation("2006-01-02T15:04", raw, time.Local); err == nil {
			v.Set(reflect.ValueOf(t))
		}
		return
	case durationType:
		if raw == "" {
			v.Set(reflect.Zero(durationType))
			return
		}
		if d, err := time.ParseDuration(raw); err == nil {
			v.Set(reflect.ValueOf(d))
		}
		return
	}

	switch v.Kind() {
	case reflect.Map, reflect.Struct:
		ptr := reflect.New(v.Type())
		if err := json.Unmarshal([]byte(raw), ptr.Interface()); err == nil {
			v.Set(ptr.Elem())
		}
	case reflect.Slice:
		setSliceField(v, raw)
	case reflect.String:
		v.SetString(raw)
	case reflect.Bool:
		v.SetBool(raw == "true" || raw == "on" || raw == "1")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			v.SetInt(n)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			v.SetUint(n)
		}
	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			v.SetFloat(f)
		}
	}
}

// setSliceField parses a comma-separated string into v (a reflect.Slice value).
func setSliceField(v reflect.Value, raw string) {
	elemType := v.Type().Elem()
	slice := reflect.MakeSlice(v.Type(), 0, 8)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		elem := reflect.New(elemType).Elem()
		setField(elem, elemType, part)
		slice = reflect.Append(slice, elem)
	}
	v.Set(slice)
}

// ─── Misc helpers ─────────────────────────────────────────────────────────────

// deepCopy returns a deep copy of *src via JSON round-trip.
func deepCopy[T any](src *T) T {
	data, _ := json.Marshal(src)
	var dst T
	_ = json.Unmarshal(data, &dst)
	return dst
}

// sliceToStrings converts each element of a slice to its string representation.
func sliceToStrings(v reflect.Value) []string {
	out := make([]string, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		out = append(out, scalarToString(v.Index(i)))
	}
	return out
}

// marshalJSON encodes v to indented JSON without HTML-escaping (<, >, &).
func marshalJSON(v interface{}) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "{}"
	}
	return strings.TrimRight(buf.String(), "\n")
}

func scalarToString(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	default:
		return ""
	}
}
