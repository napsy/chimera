package webkit

/*
#cgo pkg-config: webkit2gtk-4.1
#include <stdlib.h>
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

static GtkWidget* chimera_webview_new() {
    return GTK_WIDGET(webkit_web_view_new());
}

static void chimera_webview_load_html(WebKitWebView* view, const gchar* content, const gchar* base_uri) {
    webkit_web_view_load_html(view, content, base_uri);
}

extern gboolean goChimeraDecidePolicy(WebKitWebView*, WebKitPolicyDecision*, WebKitPolicyDecisionType, gpointer);

static void chimera_webview_connect_decide_policy(WebKitWebView* view) {
    g_signal_connect(view, "decide-policy", G_CALLBACK(goChimeraDecidePolicy), NULL);
}

static const gchar* chimera_navigation_policy_uri(WebKitPolicyDecision* decision) {
    if (!WEBKIT_IS_NAVIGATION_POLICY_DECISION(decision)) {
        return NULL;
    }

    WebKitNavigationPolicyDecision* nav = WEBKIT_NAVIGATION_POLICY_DECISION(decision);
    WebKitNavigationAction* action = webkit_navigation_policy_decision_get_navigation_action(nav);
    if (action == NULL) {
        return NULL;
    }

    WebKitURIRequest* req = webkit_navigation_action_get_request(action);
    if (req == NULL) {
        return NULL;
    }

    return webkit_uri_request_get_uri(req);
}
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
)

// WebView wraps a WebKitWebView for GTK integration.
type WebView struct {
	widget  *gtk.Widget
	view    *C.WebKitWebView
	navOnce sync.Once
}

// NewWebView constructs a new WebKit web view widget.
func NewWebView() (*WebView, error) {
	raw := C.chimera_webview_new()
	if raw == nil {
		return nil, errors.New("failed to create WebKitWebView")
	}
	obj := glib.Take(unsafe.Pointer(raw))
	widget := &gtk.Widget{glib.InitiallyUnowned{obj}}
	return &WebView{
		widget: widget,
		view:   (*C.WebKitWebView)(unsafe.Pointer(raw)),
	}, nil
}

// Widget exposes the underlying GTK widget for packing into containers.
func (w *WebView) Widget() *gtk.Widget {
	return w.widget
}

// LoadHTML renders the provided HTML content.
func (w *WebView) LoadHTML(html string, baseURI string) {
	cHTML := C.CString(html)
	defer C.free(unsafe.Pointer(cHTML))

	var cBase *C.char
	if baseURI != "" {
		cBase = C.CString(baseURI)
		defer C.free(unsafe.Pointer(cBase))
	}

	C.chimera_webview_load_html(w.view, (*C.gchar)(cHTML), (*C.gchar)(cBase))
}

// OnNavigate registers a callback that fires when the user requests a new navigation.
// Returning true from the handler signals that the navigation was handled and should not proceed.
func (w *WebView) OnNavigate(handler func(uri string) bool) {
	key := uintptr(unsafe.Pointer(w.view))
	navigationHandlers.Store(key, handler)
	w.navOnce.Do(func() {
		C.chimera_webview_connect_decide_policy(w.view)
	})
}

var navigationHandlers sync.Map

func lookupNavigationHandler(view *C.WebKitWebView) (func(string) bool, bool) {
	key := uintptr(unsafe.Pointer(view))
	if cb, ok := navigationHandlers.Load(key); ok {
		if fn, ok := cb.(func(string) bool); ok {
			return fn, true
		}
	}
	return nil, false
}

//export goChimeraDecidePolicy
func goChimeraDecidePolicy(view *C.WebKitWebView, decision *C.WebKitPolicyDecision, decisionType C.WebKitPolicyDecisionType, _ C.gpointer) C.gboolean {
	handler, ok := lookupNavigationHandler(view)
	if !ok {
		return C.FALSE
	}

	if decisionType != C.WEBKIT_POLICY_DECISION_TYPE_NAVIGATION_ACTION {
		return C.FALSE
	}

	uriC := C.chimera_navigation_policy_uri(decision)
	if uriC == nil {
		return C.FALSE
	}

	uri := C.GoString(uriC)
	if uri == "" {
		return C.FALSE
	}

	if handler(uri) {
		C.webkit_policy_decision_ignore(decision)
		return C.TRUE
	}

	return C.FALSE
}
