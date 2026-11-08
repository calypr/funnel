package plugin

var (
	_ Authorize = (*RPCClient)(nil)
	_ Authorize = (*GRPCClient)(nil)
)
