package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);
typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* host_api;

static void set_host_api(const cliproxy_host_api* host) { host_api = host; }

static void call_host(const char* method, const uint8_t* req, size_t len) {
	if (host_api == NULL || host_api->call == NULL) return;
	cliproxy_buffer resp = {0, 0};
	host_api->call(host_api->host_ctx, method, req, len, &resp);
	if (resp.ptr != NULL && host_api->free_buffer != NULL) host_api->free_buffer(resp.ptr, resp.len);
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const statusPath = "/plugins/paced-affinity/status" // GET /v0/management/plugins/paced-affinity/status

type plugin struct {
	cfg atomic.Pointer[config]
	r   *router
}

func newPlugin() *plugin {
	p := &plugin{r: newRouter()}
	cfg, _ := parseConfig(nil)
	p.cfg.Store(cfg)
	return p
}

var global = newPlugin()

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, api *C.cliproxy_plugin_api) C.int {
	if api == nil {
		return 1
	}
	C.set_host_api(host)
	api.abi_version = C.uint32_t(pluginabi.ABIVersion)
	api.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	api.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	api.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, req *C.uint8_t, n C.size_t, resp *C.cliproxy_buffer) C.int {
	if method == nil || resp == nil {
		return 1
	}
	now := float64(time.Now().UnixNano()) / 1e9
	out := global.dispatch(C.GoString(method), C.GoBytes(unsafe.Pointer(req), C.int(n)), now)
	resp.ptr, resp.len = C.CBytes(out), C.size_t(len(out))
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) { C.free(ptr) }

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

// dispatch serves one ABI call at unix time now (seconds). It never panics, and a pick that fails for any
// reason is answered "not handled", so the built-in selector (and its session affinity) routes instead.
func (p *plugin) dispatch(method string, req []byte, now float64) (out []byte) {
	defer func() {
		if v := recover(); v != nil {
			hostLog("warn", fmt.Sprintf("paced-affinity: recovered from a panic in %s: %v", method, v))
			out = envelope(nil, fmt.Errorf("panic: %v", v))
			if method == pluginabi.MethodSchedulerPick {
				out = envelope(pluginapi.SchedulerPickResponse{}, nil)
			}
		}
	}()
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		cfg, err := parseConfig(req)
		if err == nil {
			p.cfg.Store(cfg)
		}
		return envelope(registration(), err)
	case pluginabi.MethodSchedulerPick:
		return envelope(p.pick(req, now), nil)
	case pluginabi.MethodUsageHandle:
		var rec pluginapi.UsageRecord
		if json.Unmarshal(req, &rec) == nil {
			p.r.observe(&rec, now)
		}
		return envelope(struct{}{}, nil)
	case pluginabi.MethodManagementRegister:
		return envelope(map[string]any{"routes": []map[string]string{{"Method": http.MethodGet, "Path": statusPath}}}, nil)
	case pluginabi.MethodManagementHandle:
		status := p.r.status(now / 60)
		status["claude_placement"] = p.cfg.Load().ClaudePlacement
		body, err := json.Marshal(status)
		return envelope(pluginapi.ManagementResponse{StatusCode: http.StatusOK,
			Headers: http.Header{"Content-Type": {"application/json"}}, Body: body}, err)
	case pluginabi.MethodPluginQuiesce, pluginabi.MethodPluginShutdown:
		return envelope(struct{}{}, nil)
	}
	return envelope(nil, fmt.Errorf("unknown method %q", method))
}

func (p *plugin) pick(raw []byte, now float64) pluginapi.SchedulerPickResponse {
	var req pluginapi.SchedulerPickRequest
	if json.Unmarshal(raw, &req) != nil {
		return pluginapi.SchedulerPickResponse{}
	}
	cfg := p.cfg.Load()
	provider := pickProvider(&req)
	sid, _ := req.Options.Metadata["canonical_session_id"].(string)
	parent, _ := req.Options.Metadata["parent_session_id"].(string)
	if sid == "" || !cfg.routes(provider) {
		return pluginapi.SchedulerPickResponse{}
	}
	a, reason := p.r.pick(provider, sid, parent, req.Candidates, now/60, cfg.ClaudePlacement)
	resp := pluginapi.SchedulerPickResponse{AuthID: a.id, Handled: !cfg.Shadow} // shadow: decided, not routed
	// CPA's log formatter drops unknown fields, so the decision goes into the message.
	hostLog("debug", fmt.Sprintf("paced-affinity: %s -> %s (%s of %d, handled=%v)", sid, a.id, reason,
		len(req.Candidates), resp.Handled))
	return resp
}

type accountStatus struct {
	ID             string  `json:"id"`
	Provider       string  `json:"provider"`
	Util5h         float64 `json:"util_5h"`
	Reset5h        string  `json:"reset_5h,omitempty"`
	UtilWeekly     float64 `json:"util_weekly"`
	ResetWeekly    string  `json:"reset_weekly,omitempty"`
	Headroom5h     float64 `json:"headroom_5h"`     // unused fraction projected at the 5h reset at the current burn
	HeadroomWeekly float64 `json:"headroom_weekly"` // ... over the weekly look-ahead
	Exhausted      bool    `json:"exhausted"`
}

func (r *router) status(t float64) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	accounts := []accountStatus{}
	for _, id := range slices.Sorted(maps.Keys(r.accounts)) {
		a := r.accounts[id]
		p := a.project(t, 0)
		accounts = append(accounts, accountStatus{ID: a.id, Provider: a.provider,
			Util5h: a.u5, Reset5h: isoMinutes(a.r5), UtilWeekly: a.uW, ResetWeekly: isoMinutes(a.rW),
			Headroom5h: 1 - p.proj5, HeadroomWeekly: 1 - p.projW, Exhausted: a.exhausted(t)})
	}
	live := 0
	for _, b := range r.bindings {
		if t-b.lastT <= affinityTTL {
			live++
		}
	}
	return map[string]any{"live_bindings": live, "accounts": accounts}
}

func isoMinutes(m float64) string {
	if m < 0 {
		return ""
	}
	return time.Unix(int64(m*60), 0).UTC().Format(time.RFC3339)
}

func envelope(result any, err error) []byte {
	raw, errMarshal := json.Marshal(result)
	if err == nil {
		err = errMarshal
	}
	env := pluginabi.Envelope{OK: true, Result: raw}
	if err != nil {
		env = pluginabi.Envelope{Error: &pluginabi.Error{Code: "paced_affinity", Message: err.Error()}}
	}
	out, _ := json.Marshal(env)
	return out
}

func hostLog(level, msg string) {
	raw, _ := json.Marshal(map[string]string{"level": level, "message": msg})
	method := C.CString(pluginabi.MethodHostLog)
	defer C.free(unsafe.Pointer(method))
	req := C.CBytes(raw)
	defer C.free(req)
	C.call_host(method, (*C.uint8_t)(req), C.size_t(len(raw)))
}
