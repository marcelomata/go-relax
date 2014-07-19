// Copyright 2014 Codehack.com All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package relax

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"net/http"
	"strings"
)

// FilterETag generates an entity-tag header "ETag" for body content of a response.
// It will use pre-generated etags from the underlying filters or handlers, if availble.
// Optionally, it will also handle the conditional response based on If-Match
// and If-None-Match checks on specific entity-tag values.
// This implementation follows the description from RFC 7232 -
// http://tools.ietf.org/html/rfc7232
type FilterETag struct {
	// DisableConditionals will make this filter ignore the values from the headers
	// If-None-Match and If-Match and not do conditional entity tests. An ETag will
	// still be generated, if possible.
	// Defaults to false
	DisableConditionals bool
}

// strongCmp does strong comparison of If-Match entity values.
func strongCmp(etags, etag string) bool {
	if etag == "" || strings.HasPrefix(etag, "W/") {
		return false
	}
	for _, v := range strings.SplitAfter(etags, ",") {
		if strings.TrimSpace(v) == etag {
			return true
		}
	}
	return false
}

// Run runs the filter and passes down the following Info:
//		re.Info.Get("etag.enabled") // boolean; true if etag is enabled (always)
func (self *FilterETag) Run(next HandlerFunc) HandlerFunc {
	return func(rw ResponseWriter, re *Request) {
		var etag string
		re.Info.Set("etag.enabled", true)

		rr := NewResponseRewriter(bytes.NewBuffer(nil), rw.(*responseWriter).w)
		defer rr.Free()

		rw.(*responseWriter).w = rr
		next(rw, re)
		rw.(*responseWriter).w = rr.ResponseWriter

		// Do not pass GO. Do not collect $200
		if rw.Status() < 200 || rw.Status() == http.StatusNoContent ||
			(rw.Status() > 299 && rw.Status() != http.StatusPreconditionFailed) ||
			!strings.Contains("DELETE GET HEAD PATCH POST PUT", re.Method) {
			Log.Printf(LOG_DEBUG, "%s FilterETag: no ETag generated (status=%d method=%s)", re.Info.Get("context.request_id"), rw.Status(), re.Method)
			goto Finish
		}

		etag = rw.Header().Get("ETag")

		if (re.Method == "GET" || re.Method == "HEAD") && rw.Status() == http.StatusOK {
			if etag == "" {
				// change etag when using compression
				alter := ""
				if ct := re.Info.Get("compress.type"); ct != "" {
					alter = "-" + ct
				}
				etag = fmt.Sprintf(`"%x%s"`, sha1.Sum(rr.Writer.(*bytes.Buffer).Bytes()), alter)
			}
		}

		if !self.DisableConditionals {
			ifnone, ifmatch := re.Header.Get("If-None-Match"), re.Header.Get("If-Match")
			if ifmatch != "" && ((ifmatch == "*" && etag == "") || !strongCmp(ifmatch, etag)) {
				/* FIXME: need to verify Status per request.
				if strings.Contains("DELETE PATCH POST PUT", re.Method) && rw.Status() != http.StatusPreconditionFailed {
					// XXX: we cant confirm it's the same resource item without re-GET'ing it.
					// XXX: maybe etag should be changed from strong to weak.
					etag = ""
					Log.Printf(LOG_DEBUG, "%s FilterETag: no ETag generated (status=%d method=%s)", re.Info.Get("context.request_id"), rw.Status(), re.Method)
					goto Finish
				}
				*/
				rw.WriteHeader(http.StatusPreconditionFailed)
				rr.Writer.(*bytes.Buffer).Reset()
				return
			}

			// BUG(TODO): FilterETag should have support for conditionals If-Modified-Since,
			// If-Unmodified-Since, and/or Range/If-Range.

			if ifnone != "" && ((ifnone == "*" && etag != "") || strings.Contains(ifnone, etag)) {
				if re.Method == "GET" || re.Method == "HEAD" {
					rw.Header().Set("ETag", etag)
					rw.Header().Add("Vary", "If-None-Match")
					rw.WriteHeader(http.StatusNotModified)
					rr.Writer.(*bytes.Buffer).Reset()
					return
				}
				rw.WriteHeader(http.StatusPreconditionFailed)
				rr.Writer.(*bytes.Buffer).Reset()
				return
			}
		}
	Finish:
		if etag != "" {
			rw.Header().Set("ETag", etag)
			rw.Header().Add("Vary", "If-None-Match")
		}
		rr.Writer.(*bytes.Buffer).WriteTo(rw)
	}
}
