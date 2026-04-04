package job

import (
	"math"
	"testing"
)

// =============================================================================
// TestCalculateStockResult — คำนวณผลหวยหุ้นจากดัชนี
// ⚠️ จ่ายผิดเลข = จ่ายเงินผิดคน — ต้องแม่นยำ 100%
// =============================================================================

func TestCalculateStockResult(t *testing.T) {
	tests := []struct {
		name       string
		indexValue string
		wantTop3   string
		wantTop2   string
		wantBot2   string
	}{
		// ─── เคสปกติ ─────────────────────────────────────────
		{"SET normal", "1432.56", "256", "56", "56"},
		{"SET high", "1678.93", "893", "93", "93"},
		{"SET low", "998.12", "812", "12", "12"},
		{"SET round", "1500.00", "000", "00", "00"},

		// ─── เลขลงท้ายด้วย 0 ────────────────────────────────
		{"trailing zero decimal", "1432.50", "250", "50", "50"},
		{"trailing zero integer", "1430.56", "056", "56", "56"},

		// ─── ดัชนีมี comma ──────────────────────────────────
		{"with comma", "1,432.56", "256", "56", "56"},
		{"with comma high", "12,345.67", "567", "67", "67"},

		// ─── ทศนิยม 1 ตัว ───────────────────────────────────
		{"single decimal", "1432.5", "250", "50", "50"},

		// ─── ดัชนีเล็ก (3 หลัก) ─────────────────────────────
		{"small index", "99.12", "912", "12", "12"},

		// ─── ดัชนีใหญ่ (5 หลัก) ─────────────────────────────
		{"large index", "34567.89", "789", "89", "89"},

		// ─── เลขท้าย 000 ────────────────────────────────────
		{"all zeros tail", "1000.00", "000", "00", "00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top3, top2, bot2 := calculateStockResult(tt.indexValue)

			if top3 != tt.wantTop3 {
				t.Errorf("top3 = %q, want %q (index=%s)", top3, tt.wantTop3, tt.indexValue)
			}
			if top2 != tt.wantTop2 {
				t.Errorf("top2 = %q, want %q (index=%s)", top2, tt.wantTop2, tt.indexValue)
			}
			if bot2 != tt.wantBot2 {
				t.Errorf("bottom2 = %q, want %q (index=%s)", bot2, tt.wantBot2, tt.indexValue)
			}
		})
	}
}

func TestCalculateStockResult_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"no decimal", "1432"},
		{"empty", ""},
		{"text", "abc"},
		{"just dot", "."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			top3, top2, bot2 := calculateStockResult(tt.input)
			if top3 != "" || top2 != "" || bot2 != "" {
				t.Errorf("expected empty results for invalid input %q, got top3=%q top2=%q bot2=%q",
					tt.input, top3, top2, bot2)
			}
		})
	}
}

// =============================================================================
// TestMin helper
// =============================================================================

func TestMin(t *testing.T) {
	if min(3, 5) != 3 { t.Error("min(3,5) should be 3") }
	if min(5, 3) != 3 { t.Error("min(5,3) should be 3") }
	if min(3, 3) != 3 { t.Error("min(3,3) should be 3") }
	_ = math.Abs(0) // suppress unused import
}
