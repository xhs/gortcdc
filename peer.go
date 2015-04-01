package gortcdc

import (
  "net"
  "errors"
)

type Peer struct {
  trans *transport
  stun_ip string
  stun_port int
}

func NewPeer() (*Peer, error) {
  return &Peer{stun_port: 3478}, nil
}

func (p *Peer) Destroy() {
  if p.trans != nil {
    p.trans.destroy()
  }
}

func (p *Peer) SetStunServer(server string) error {
  ips, err := net.LookupIP(server)
  if err != nil {
    return err
  }
  p.stun_ip = ips[0].String()
  return nil
}

func (p *Peer) SetStunPort(port int) error {
  p.stun_port = port
  return nil
}

func (p *Peer) GenerateOfferSdp() (string, error) {
  if p.trans == nil {
    t, err := p.newTransport()
    if err != nil {
      return "", err
    }
    p.trans = t
  }

  offer, err := p.trans.generateOfferSdp()
  if err != nil {
    return "", err
  }
  return offer, nil
}

func (p *Peer) ParseOfferSdp(offer string) (int, error) {
  if p.trans == nil {
    t, err := p.newTransport()
    if err != nil {
      return 0, err
    }
    p.trans = t
  }

  rv, err := p.trans.parseOfferSdp(offer)
  if err != nil {
    return 0, err
  }
  return rv, nil
}

func (p *Peer) ParseCandidateSdp(candidates string) (int, error) {
  if p.trans == nil {
    return 0, errors.New("transport nil")
  }
  return p.trans.parseCandidateSdp(candidates), nil
}
