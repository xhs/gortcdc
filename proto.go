package gortcdc

import (
  "math/rand"
  "time"
  "fmt"
  "strings"
  "strconv"
  "encoding/json"

  "github.com/xhs/goblice"
  "github.com/xhs/godtls"
  "github.com/xhs/gosctp"
)

const (
  roleClient = 0
  roleServer = 1
)

type Peer struct {
  ice *goblice.Agent
  ctx *godtls.DtlsContext
  dtls *godtls.DtlsTransport
  sctp *gosctp.SctpTransport
  role int
  remotePort int
}

func NewPeer() (*Peer, error) {
  ice, err := goblice.NewReliableAgent()
  if err != nil {
    return nil, err
  }
  ctx, err := godtls.NewContext("gortcdc", 365)
  if err != nil {
    ice.Destroy()
    return nil, err
  }
  dtls, err := ctx.NewTransport()
  if err != nil {
    ice.Destroy()
    ctx.Destroy()
    return nil, err
  }
  rand.Seed(time.Now().UnixNano())
  sctp, err := gosctp.NewTransport(rand.Intn(50001) + 10000)
  if err != nil {
    ice.Destroy()
    ctx.Destroy()
    dtls.Destroy()
    return nil, err
  }
  p := &Peer{
    ice: ice,
    ctx: ctx,
    dtls: dtls,
    sctp: sctp,
    role: roleClient,
  }
  return p, nil
}

func (p *Peer) Destroy() {
  p.ice.Destroy()
  p.dtls.Destroy()
  p.ctx.Destroy()
  p.sctp.Destroy()
}

type Signaller interface {
  Send(data []byte) error
  ReceiveFrom() <-chan []byte
}

type Signal struct {
  Type string `json:"type"`
  Value string `json:"value"`
}

func (p *Peer) Run(s Signaller) error {
  if err := p.ice.GatherCandidates(); err != nil {
    return err
  }
  go p.ice.Run()

  signalChan := s.ReceiveFrom()
  for {
    select {
    case cand := <-p.ice.Candidates:
      candidate, err := json.Marshal(&Signal{"candidate", cand})
      if err != nil {
        return err
      }
      s.Send(candidate)
      continue
    case e := <-p.ice.Events:
      if e == goblice.EventNegotiationDone {
        break
      }
      continue
    case data := <-signalChan:
      sig := &Signal{}
      if err := json.Unmarshal(data, sig); err != nil {
        continue
      }
      if sig.Type == "candidate" {
        p.ParseCandidateSdp(sig.Value)
      } else if sig.Type == "offer" {
        p.ParseOfferSdp(sig.Value)
        sdp, err := p.GenerateOfferSdp()
        if err != nil {
          return err
        }
        answer, err := json.Marshal(&Signal{"answer", sdp})
        if err != nil {
          return err
        }
        s.Send(answer)
      } else if sig.Type == "answer" {
        p.ParseOfferSdp(sig.Value)
      }
      continue
    }
    break
  }

  if p.role == roleClient {
    p.dtls.SetConnectState()
  } else {
    p.dtls.SetAcceptState()
  }

  go func () {
    for {
      data := <-p.ice.DataToRead
      p.dtls.Feed(data)
    }
  }()

  go func () {
    var buf [1 << 16]byte
    tick := time.Tick(2 * time.Millisecond)
    for {
      <-tick
      n, _ := p.dtls.Spew(buf[:])
      if n > 0 {
        p.ice.Send(buf[:n])
      }
    }
  }()

  if err := p.dtls.Handshake(); err != nil {
    return err
  }

  return nil
}

var numbers = []rune("0123456789")

func randSession() string {
  s := make([]rune, 16)
  rand.Seed(time.Now().UnixNano())
  for i := range s {
    s[i] = numbers[rand.Intn(10)]
  }
  return string(s)
}

func (p *Peer) GenerateOfferSdp() (string, error) {
  var offer []string
  offer = append(offer, "v=0")
  offer = append(offer, fmt.Sprintf("o=- %s 2 IN IP4 127.0.0.1", randSession()))
  offer = append(offer, "s=-")
  offer = append(offer, "t=0 0")
  offer = append(offer, "a=msid-semantic: WMS")
  offer = append(offer, fmt.Sprintf("m=application 1 DTLS/SCTP %d", p.sctp.Port))
  offer = append(offer, "c=IN IP4 0.0.0.0")

  sdp := p.ice.GenerateSdp()
  sdps := strings.Split(sdp, "\n")
  for i := range sdps {
    if strings.HasPrefix(sdps[i], "a=ice-ufrag:") {
      offer = append(offer, sdps[i])
    } else if strings.HasPrefix(sdps[i], "a=ice-pwd:") {
      offer = append(offer, sdps[i])
    }
  }

  offer = append(offer, fmt.Sprintf("a=fingerprint:sha-256 %s", p.dtls.Fingerprint))
  if p.role == roleClient {
    offer = append(offer, "a=setup:active")
  } else {
    offer = append(offer, "a=setup:passive")
  }
  offer = append(offer, "a=mid:data")
  offer = append(offer, fmt.Sprintf("a=sctpmap:%d webrtc-datachannel 1024", p.sctp.Port))

  offer = append(offer, "")
  return strings.Join(offer, "\r\n"), nil
}

func (p *Peer) ParseOfferSdp(offer string) (int, error) {
  sdps := strings.Split(offer, "\r\n")
  for i := range sdps {
    if strings.HasPrefix(sdps[i], "a=sctpmap:") {
      sctpmap := strings.Split(sdps[i], " ")[0]
      port, err := strconv.Atoi(strings.Split(sctpmap, ":")[1])
      if err != nil {
        return 0, err
      }
      p.remotePort = port
    } else if strings.HasPrefix(sdps[i], "a=setup:active") {
      if p.role == roleClient {
        p.role = roleServer
      }
    } else if strings.HasPrefix(sdps[i], "a=setup:passive") {
      if p.role == roleServer {
        p.role = roleClient
      }
    }
  }

  rv, err := p.ice.ParseSdp(strings.Join(sdps, "\n"))
  if err != nil {
    return 0, err
  }
  return int(rv), nil
}

func (p *Peer) ParseCandidateSdp(cands string) int {
  sdps := strings.Split(cands, "\r\n")
  count := 0
  for i := range sdps {
    if sdps[i] == "" {
      continue
    }
    rv, err := p.ice.ParseCandidateSdp(sdps[i])
    if err != nil {
      continue
    }
    count = count + rv
  }
  return count
}