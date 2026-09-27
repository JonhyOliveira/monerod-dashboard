package rpc

import "context"

// Connection is a live P2P connection (get_connections, sync_info).
type Connection struct {
	Address         string `json:"address"`
	AddressType     uint8  `json:"address_type"`
	Host            string `json:"host"`
	IP              string `json:"ip"`
	Port            string `json:"port"`
	ConnectionID    string `json:"connection_id"`
	PeerID          string `json:"peer_id"`
	Incoming        bool   `json:"incoming"`
	LocalIP         bool   `json:"local_ip"`
	Localhost       bool   `json:"localhost"`
	Height          uint64 `json:"height"`
	LiveTime        uint64 `json:"live_time"`
	State           string `json:"state"`
	AvgDownload     uint64 `json:"avg_download"`
	AvgUpload       uint64 `json:"avg_upload"`
	CurrentDownload uint64 `json:"current_download"`
	CurrentUpload   uint64 `json:"current_upload"`
	RecvCount       uint64 `json:"recv_count"`
	SendCount       uint64 `json:"send_count"`
	RecvIdleTime    uint64 `json:"recv_idle_time"`
	SendIdleTime    uint64 `json:"send_idle_time"`
	PruningSeed     uint32 `json:"pruning_seed"`
	RPCPort         uint16 `json:"rpc_port"`
	SupportFlags    uint32 `json:"support_flags"`
}

// GetConnections calls get_connections.
func (c *Client) GetConnections(ctx context.Context) ([]Connection, error) {
	var r struct {
		Status
		Connections []Connection `json:"connections"`
	}
	return r.Connections, c.Call(ctx, "get_connections", nil, &r)
}

// Peer is an entry of the white or gray peer list.
type Peer struct {
	Host        string `json:"host"`
	ID          uint64 `json:"id"`
	IP          uint32 `json:"ip"`
	Port        uint16 `json:"port"`
	LastSeen    int64  `json:"last_seen"`
	PruningSeed uint32 `json:"pruning_seed"`
	RPCPort     uint16 `json:"rpc_port"`
}

// PeerList is /get_peer_list.
type PeerList struct {
	Status
	WhiteList []Peer `json:"white_list"`
	GrayList  []Peer `json:"gray_list"`
}

// GetPeerList calls /get_peer_list.
func (c *Client) GetPeerList(ctx context.Context) (*PeerList, error) {
	var r PeerList
	return &r, c.CallPath(ctx, "/get_peer_list", map[string]bool{"public_only": false}, &r)
}

// PublicNode is an entry of /get_public_nodes: peers advertising a public RPC.
type PublicNode struct {
	Host     string `json:"host"`
	LastSeen int64  `json:"last_seen"`
	RPCPort  uint16 `json:"rpc_port"`
}

// PublicNodes is /get_public_nodes.
type PublicNodes struct {
	Status
	White []PublicNode `json:"white"`
	Gray  []PublicNode `json:"gray"`
}

// GetPublicNodes calls /get_public_nodes.
func (c *Client) GetPublicNodes(ctx context.Context) (*PublicNodes, error) {
	var r PublicNodes
	return &r, c.CallPath(ctx, "/get_public_nodes", map[string]bool{"white": true, "gray": true}, &r)
}

// Ban is an entry of get_bans / set_bans. Host may be an IP or a subnet in
// CIDR form.
type Ban struct {
	Host    string `json:"host"`
	IP      uint32 `json:"ip,omitempty"`
	Ban     bool   `json:"ban"`
	Seconds uint32 `json:"seconds"`
}

// GetBans calls get_bans.
func (c *Client) GetBans(ctx context.Context) ([]Ban, error) {
	var r struct {
		Status
		Bans []Ban `json:"bans"`
	}
	return r.Bans, c.Call(ctx, "get_bans", nil, &r)
}

// SetBans calls set_bans.
func (c *Client) SetBans(ctx context.Context, bans []Ban) error {
	var r Status
	return c.Call(ctx, "set_bans", map[string][]Ban{"bans": bans}, &r)
}

// Banned calls banned: whether address is banned and for how long.
func (c *Client) Banned(ctx context.Context, address string) (banned bool, seconds uint32, err error) {
	var r struct {
		Status
		Banned  bool   `json:"banned"`
		Seconds uint32 `json:"seconds"`
	}
	err = c.Call(ctx, "banned", map[string]string{"address": address}, &r)
	return r.Banned, r.Seconds, err
}

// OutPeers calls /out_peers to set the outgoing connection limit, returning
// the new limit.
func (c *Client) OutPeers(ctx context.Context, n uint32) (uint32, error) {
	var r struct {
		Status
		OutPeers uint32 `json:"out_peers"`
	}
	return r.OutPeers, c.CallPath(ctx, "/out_peers", map[string]any{"set": true, "out_peers": n}, &r)
}

// InPeers calls /in_peers to set the incoming connection limit, returning
// the new limit.
func (c *Client) InPeers(ctx context.Context, n uint32) (uint32, error) {
	var r struct {
		Status
		InPeers uint32 `json:"in_peers"`
	}
	return r.InPeers, c.CallPath(ctx, "/in_peers", map[string]any{"set": true, "in_peers": n}, &r)
}

// PeerLimits reads the current outgoing and incoming connection limits
// (out_peers / in_peers with set=false).
func (c *Client) PeerLimits(ctx context.Context) (out, in uint32, err error) {
	var o struct {
		Status
		OutPeers uint32 `json:"out_peers"`
	}
	var i struct {
		Status
		InPeers uint32 `json:"in_peers"`
	}
	if err = c.CallPath(ctx, "/out_peers", map[string]bool{"set": false}, &o); err != nil {
		return 0, 0, err
	}
	err = c.CallPath(ctx, "/in_peers", map[string]bool{"set": false}, &i)
	return o.OutPeers, i.InPeers, err
}
