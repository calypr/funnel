package task

import (
	"strings"

	"github.com/calypr/funnel/api/tes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NormalizeView validates and canonicalizes the requested TES view.
func NormalizeView(view string) (string, error) {
	if view == "" {
		return tes.View_MINIMAL.String(), nil
	}
	normalized := strings.ToUpper(view)
	if _, ok := tes.View_value[normalized]; !ok {
		return "", status.Errorf(codes.InvalidArgument, "invalid view %q: must be one of MINIMAL, BASIC, FULL", view)
	}
	return normalized, nil
}
