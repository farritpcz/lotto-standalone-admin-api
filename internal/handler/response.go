// Package handler — response helpers (thin wrappers over lotto-core/httpx).
//
// Source of truth: github.com/farritpcz/lotto-core/httpx
// These 1-line wrappers keep call sites short (`ok(c, data)` vs `httpx.OK(c, data)`).
// If you add a new response helper, add it to lotto-core/httpx first.
package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/farritpcz/lotto-core/httpx"
)

func ok(c *gin.Context, data interface{})         { httpx.OK(c, data) }
func fail(c *gin.Context, status int, msg string) { httpx.Fail(c, status, msg) }
func paginated(c *gin.Context, items interface{}, total int64, page, perPage int) {
	httpx.Paginated(c, items, total, page, perPage)
}
func pageParams(c *gin.Context) (int, int)            { return httpx.PageParams(c) }
func parseFloat(s string, defaultVal float64) float64 { return httpx.ParseFloat(s, defaultVal) }
func strPtr(s string) *string                         { return httpx.StrPtr(s) }
