package api

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdmissionExpiresAndBoundsMemory(t *testing.T) {
	var a requestAdmission
	now := time.Now()
	if !a.allow("one", 2, 1, now) || !a.allow("one", 2, 1, now) || a.allow("one", 2, 1, now) {
		t.Fatal("count bound failed")
	}
	if a.allow("two", 2, 1, now) {
		t.Fatal("key bound failed")
	}
	if !a.allow("two", 2, 1, now.Add(time.Minute)) || len(a.entries) != 1 {
		t.Fatal("expired key was not reclaimed")
	}
}

func TestAdmissionConcurrentRequests(t *testing.T) {
	var a requestAdmission
	var accepted atomic.Int32
	var wg sync.WaitGroup
	now := time.Now()
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.allow("user", 7, 10, now) {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 7 {
		t.Fatalf("accepted %d, want 7", accepted.Load())
	}
}

func TestRequestLimitConfiguration(t *testing.T) {
	for _, key := range []string{"MAX_JSON_BYTES", "MAX_SCRIPT_BYTES", "MAX_SCRIPT_WORDS", "MAX_CHAPTERS", "MAX_TEXT_FIELD_BYTES", "MAX_COLOUR_IMAGE_BYTES", "MAX_COLOUR_IMAGE_PIXELS", "REQUESTS_PER_IP_PER_MINUTE", "REQUESTS_PER_USER_PER_MINUTE", "REQUEST_ADMISSION_KEYS"} {
		t.Run(key, func(t *testing.T) {
			limits, err := RequestLimitsFromEnv(func(name string) string {
				if name == key {
					return "123"
				}
				return ""
			})
			if err != nil || limits == DefaultRequestLimits() {
				t.Fatalf("override ignored: %+v %v", limits, err)
			}
			for _, raw := range []string{"0", "-1", "oops", "9223372036854775808"} {
				if _, err := RequestLimitsFromEnv(func(name string) string {
					if name == key {
						return raw
					}
					return ""
				}); err == nil {
					t.Fatal(fmt.Sprintf("accepted %s=%s", key, raw))
				}
			}
		})
	}
}
