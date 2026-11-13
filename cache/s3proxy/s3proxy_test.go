package s3proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/buchgr/bazel-remote/v2/cache"
	testutils "github.com/buchgr/bazel-remote/v2/utils"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestObjectKey(t *testing.T) {
	testCases := []struct {
		prefix     string
		key        string
		kind       cache.EntryKind
		expectedV1 string
		expectedV2 string
	}{
		{"", "1234", cache.CAS, "cas/12/1234", "cas.v2/12/1234"},
		{"test", "1234", cache.CAS, "test/cas/12/1234", "test/cas.v2/12/1234"},
		{"foo/bar/grok", "1234", cache.CAS, "foo/bar/grok/cas/12/1234", "foo/bar/grok/cas.v2/12/1234"},
		{"", "1234", cache.AC, "ac/12/1234", "ac/12/1234"},
		{"", "1234", cache.RAW, "raw/12/1234", "raw/12/1234"},
		{"foo/bar", "1234", cache.AC, "foo/bar/ac/12/1234", "foo/bar/ac/12/1234"},
	}

	for _, tc := range testCases {
		result := objectKeyV2(tc.prefix, tc.key, tc.kind)
		if result != tc.expectedV2 {
			t.Errorf("objectKeyV2 did not match. (result: '%s' expected: '%s'",
				result, tc.expectedV2)
		}

		result = objectKeyV1(tc.prefix, tc.key, tc.kind)
		if result != tc.expectedV1 {
			t.Errorf("objectKeyV1 did not match. (result: '%s' expected: '%s'",
				result, tc.expectedV1)
		}
	}
}

func TestRequestMetrics(t *testing.T) {
	const present = "0000000000000000000000000000000000000000000000000000000000000001"
	const absent = "0000000000000000000000000000000000000000000000000000000000000002"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, present) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := testutils.NewSilentLogger()
	proxy := New(
		strings.TrimPrefix(server.URL, "http://"),
		"bucket",
		minio.BucketLookupPath,
		"",
		credentials.NewStaticV4("id", "secret", ""),
		true,  // DisableSSL
		false, // UpdateTimestamps
		"us-east-1",
		1, // MaxIdleConns
		"zstd", logger, logger, 0, 0)

	found := requests.WithLabelValues("head", "200")
	notFound := requests.WithLabelValues("head", "404")
	foundBefore := testutil.ToFloat64(found)
	notFoundBefore := testutil.ToFloat64(notFound)

	if ok, _ := proxy.Contains(context.Background(), cache.CAS, present, -1); !ok {
		t.Error("expected the s3 backend to contain the blob")
	}
	if ok, _ := proxy.Contains(context.Background(), cache.CAS, absent, -1); ok {
		t.Error("expected the s3 backend not to contain the blob")
	}

	if n := testutil.ToFloat64(found) - foundBefore; n != 1 {
		t.Errorf("expected 1 HEAD request with status 200, counted %v", n)
	}
	if n := testutil.ToFloat64(notFound) - notFoundBefore; n != 1 {
		t.Errorf("expected 1 HEAD request with status 404, counted %v", n)
	}
	if testutil.CollectAndCount(requestDuration) == 0 {
		t.Error("expected the request duration to be recorded")
	}
}
