package overlay

import "testing"

func TestDock(t *testing.T) {
	work := Rect{0, 0, 1920, 1040} // 1080p less the taskbar
	tests := []struct {
		name   string
		client Rect
		work   Rect
		width  int32
		want   Rect
	}{
		// The default client size, centered: 320px free each side.
		{"1280x720 centered", Rect{320, 180, 1600, 900}, work, 320, Rect{1600, 180, 1920, 900}},
		{"1024x576 centered", Rect{448, 232, 1472, 808}, work, 320, Rect{1472, 232, 1792, 808}},
		{"right full, left free", Rect{560, 100, 1840, 820}, work, 320, Rect{240, 100, 560, 820}},
		{"no room either side", Rect{320, 180, 1600, 900}, work, 400, Rect{1200, 180, 1600, 900}},
		{"fills the screen", Rect{0, 0, 1920, 1040}, work, 320, Rect{1600, 0, 1920, 1040}},
		{"second monitor", Rect{2368, 232, 3392, 808}, Rect{1920, 0, 3840, 1040}, 320, Rect{3392, 232, 3712, 808}},
		{"second monitor, left edge", Rect{2240, 0, 3840, 900}, Rect{1920, 0, 3840, 1040}, 320, Rect{1920, 0, 2240, 900}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Dock(tt.client, tt.work, tt.width); got != tt.want {
				t.Errorf("Dock(%v, %v, %d) = %v, want %v", tt.client, tt.work, tt.width, got, tt.want)
			}
		})
	}
}
