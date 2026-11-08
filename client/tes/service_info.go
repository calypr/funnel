package tes

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/calypr/funnel/tes"
)

// GetServiceInfo returns result of GET /v1/service-info
func (c *Client) GetServiceInfo(ctx context.Context, req *tes.GetServiceInfoRequest) (*tes.ServiceInfo, error) {
	u := c.address + "/v1/service-info"
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("tes: creating GET request: %w", err)
	}
	body, err := checkHTTPResponse(c.do(hreq))
	if err != nil {
		return nil, err
	}

	// Parse response
	resp := &tes.ServiceInfo{}
	err = protojson.Unmarshal(body, resp)
	if err != nil {
		return nil, err
	}
	return resp, nil
}
