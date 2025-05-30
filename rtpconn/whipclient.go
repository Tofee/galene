package rtpconn

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/jech/galene/conn"
	"github.com/jech/galene/group"
	"github.com/jech/galene/sdpfrag"

	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

type WhipClient struct {
	group    *group.Group
	addr     net.Addr
	id       string
	token    string
	username string

	mu          sync.Mutex
	permissions []string
	connection  *rtpUpConnection
	etag        string
}

func NewWhipClient(g *group.Group, id string, token string, addr net.Addr) *WhipClient {
	return &WhipClient{group: g, id: id, token: token, addr: addr}
}

func (c *WhipClient) Group() *group.Group {
	return c.group
}

func (c *WhipClient) Addr() net.Addr {
	return c.addr
}

func (c *WhipClient) Id() string {
	return c.id
}

func (c *WhipClient) Token() string {
	return c.token
}

func (c *WhipClient) Username() string {
	return c.username
}

func (c *WhipClient) SetUsername(username string) {
	c.username = username
}

func (c *WhipClient) Permissions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.permissions
}

func (c *WhipClient) SetPermissions(perms []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.permissions = perms
}

func (c *WhipClient) Data() map[string]interface{} {
	return nil
}

func (c *WhipClient) ETag() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.etag
}

func (c *WhipClient) SetETag(etag string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.etag = etag
}

func (c *WhipClient) PushConn(g *group.Group, id string, conn conn.Up, tracks []conn.UpTrack, replace string) error {
	return nil
}

func (c *WhipClient) RequestConns(target group.Client, g *group.Group, id string) error {
	if g != c.group {
		return nil
	}

	c.mu.Lock()
	up := c.connection
	c.mu.Unlock()
	if up == nil {
		return nil
	}
	tracks := up.getTracks()
	ts := make([]conn.UpTrack, len(tracks))
	for i, t := range tracks {
		ts[i] = t
	}
	target.PushConn(g, up.Id(), up, ts, "")
	return nil
}

func (c *WhipClient) Joined(group, kind string) error {
	return nil
}

func (c *WhipClient) PushClient(group, kind, id, username string, permissions []string, status map[string]interface{}) error {
	return nil
}

func (c *WhipClient) Kick(id string, user *string, message string) error {
	return c.Close()
}

func (c *WhipClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.group
	if g == nil {
		return nil
	}
	if c.connection != nil {
		id := c.connection.Id()
		c.connection.pc.OnICEConnectionStateChange(nil)
		c.connection.pc.Close()
		c.connection = nil
		for _, c := range g.GetClients(c) {
			c.PushConn(g, id, nil, nil, "")
		}
		c.connection = nil
	}
	group.DelClient(c)
	c.group = nil
	return nil
}

func (c *WhipClient) NewConnection(ctx context.Context, offer []byte) ([]byte, error) {
	conn, err := newUpConn(c, c.id, "", string(offer))
	if err != nil {
		return nil, err
	}

	conn.pc.OnICEConnectionStateChange(
		func(state webrtc.ICEConnectionState) {
			switch state {
			case webrtc.ICEConnectionStateFailed,
				webrtc.ICEConnectionStateClosed:
				c.Close()
			}
		})

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connection != nil {
		conn.pc.OnICEConnectionStateChange(nil)
		conn.pc.Close()
		return nil, errors.New("duplicate connection")
	}
	c.connection = conn

	answer, err := c.gotOffer(ctx, offer)
	if err != nil {
		conn.pc.OnICEConnectionStateChange(nil)
		conn.pc.Close()
		return nil, err
	}

	return answer, nil
}

func (c *WhipClient) GotOffer(ctx context.Context, offer []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gotOffer(ctx, offer)
}

func (c *WhipClient) UFragPwd() (string, string, error) {
	c.mu.Lock()
	conn := c.connection
	c.mu.Unlock()
	if conn == nil {
		return "", "", errors.New("no connection in WHIP client")
	}

	rs := conn.pc.GetReceivers()
	if len(rs) < 1 {
		return "", "", errors.New("no receivers in PeerConnection")
	}

	parms, err := rs[0].Transport().ICETransport().GetRemoteParameters()
	if err != nil {
		return "", "", err
	}

	return parms.UsernameFragment, parms.Password, nil

}

// called locked
func (c *WhipClient) gotOffer(ctx context.Context, offer []byte) ([]byte, error) {
	conn := c.connection
	err := conn.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(offer),
	})
	if err != nil {
		return nil, err
	}

	answer, err := conn.pc.CreateAnswer(nil)
	if err != nil {
		return nil, err
	}

	gatherComplete := webrtc.GatheringCompletePromise(conn.pc)

	err = conn.pc.SetLocalDescription(answer)
	if err != nil {
		return nil, err
	}

	conn.flushICECandidates()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-gatherComplete:
	}

	return []byte(conn.pc.CurrentLocalDescription().SDP), nil
}

func (c *WhipClient) GotICECandidate(init webrtc.ICECandidateInit) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connection == nil {
		return nil
	}
	return c.connection.addICECandidate(&init)
}

func (c *WhipClient) Restart(ctx context.Context, frag sdpfrag.SDPFrag) (sdpfrag.SDPFrag, error) {
	c.mu.Lock()
	conn := c.connection
	c.mu.Unlock()
	if conn == nil {
		return sdpfrag.SDPFrag{}, errors.New("no connection")
	}

	offer := conn.pc.RemoteDescription()
	var sdpOffer sdp.SessionDescription
	err := sdpOffer.Unmarshal([]byte(offer.SDP))
	if err != nil {
		return sdpfrag.SDPFrag{}, nil
	}
	sdpOffer2, _ := sdpfrag.PatchSDP(sdpOffer, frag)
	offer2, err := sdpOffer2.Marshal()
	if err != nil {
		return sdpfrag.SDPFrag{}, nil
	}
	err = conn.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  string(offer2),
	})
	if err != nil {
		return sdpfrag.SDPFrag{}, err
	}

	answer, err := conn.pc.CreateAnswer(nil)
	if err != nil {
		return sdpfrag.SDPFrag{}, err
	}

	gatherComplete := webrtc.GatheringCompletePromise(conn.pc)

	err = conn.pc.SetLocalDescription(answer)
	if err != nil {
		return sdpfrag.SDPFrag{}, err
	}

	select {
	case <-ctx.Done():
		return sdpfrag.SDPFrag{}, ctx.Err()
	case <-gatherComplete:
	}

	sdpAnswer2 := conn.pc.LocalDescription()
	var answer2 sdp.SessionDescription
	err = answer2.Unmarshal([]byte(sdpAnswer2.SDP))
	if err != nil {
		return sdpfrag.SDPFrag{}, err
	}
	return sdpfrag.FromSDP(answer2), nil
}
