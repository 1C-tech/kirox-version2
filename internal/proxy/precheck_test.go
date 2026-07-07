package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPreCheckProxy_Empty(t *testing.T) {
	err := PreCheckProxy("")
	if err != nil {
		t.Errorf("expected nil for empty proxy, got %v", err)
	}
}

func TestPreCheckProxy_US_Success(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("loc=US\nip=1.2.3.4\ncolo=LAX\n"))
	}))
	defer ts.Close()

	err := preCheckProxyImpl("", ts.URL)
	if err != nil {
		t.Errorf("expected nil for US proxy, got %v", err)
	}
}

func TestPreCheckProxy_CN_Blocked(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("loc=CN\nip=5.6.7.8\ncolo=HKG\n"))
	}))
	defer ts.Close()

	err := preCheckProxyImpl("", ts.URL)
	if err == nil {
		t.Fatal("expected ErrRegionBlocked for CN proxy, got nil")
	}
	if err != ErrRegionBlocked {
		t.Errorf("expected ErrRegionBlocked, got %v", err)
	}
}
