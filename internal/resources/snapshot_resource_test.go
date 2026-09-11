package resources

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPrepareSnapshotCreateState(t *testing.T) {
	model := &snapshotModel{
		ID:        types.StringUnknown(),
		CreatedTS: types.StringUnknown(),
	}

	prepareSnapshotCreateState(model)

	if !model.ID.IsNull() {
		t.Fatalf("snapshot id should be known null before catalog read, got %#v", model.ID)
	}
	if !model.CreatedTS.IsNull() {
		t.Fatalf("snapshot created_ts should be known null before catalog read, got %#v", model.CreatedTS)
	}
}
