package clients

import "testing"

func TestSyncResultHasChanges(t *testing.T) {
	tests := []struct {
		name   string
		result SyncResult
		want   bool
	}{
		{
			name: "no changes",
			result: SyncResult{
				Added:     []string{},
				Updated:   []string{},
				Removed:   []string{},
				Unchanged: []string{"stewreads"},
			},
			want: false,
		},
		{
			name: "added change",
			result: SyncResult{
				Added: []string{"stewreads"},
			},
			want: true,
		},
		{
			name: "updated change",
			result: SyncResult{
				Updated: []string{"stewreads"},
			},
			want: true,
		},
		{
			name: "removed change",
			result: SyncResult{
				Removed: []string{"stewreads"},
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.HasChanges(); got != tc.want {
				t.Fatalf("HasChanges()=%t want %t", got, tc.want)
			}
		})
	}
}
