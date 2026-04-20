// Package handler — shared response admin handlers.
// Split from stubs.go on 2026-04-20.
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// =============================================================================
// Helper — JSON response
// =============================================================================

func ok(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}
func fail(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"success": false, "error": msg})
}
func paginated(c *gin.Context, items interface{}, total int64, page, perPage int) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"items": items, "total": total, "page": page, "per_page": perPage},
	})
}
func pageParams(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}
	return page, perPage
}

// parseFloat parse string → float64 with default value
func parseFloat(s string, defaultVal float64) float64 {
	if s == "" {
		return defaultVal
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return defaultVal
	}
	return v
}
func strPtr(s string) *string { return &s }
