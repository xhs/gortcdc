package gortcdc

import (
  "math/rand"
  "time"
  "fmt"
  "strings"
  "strconv"
  "encoding/json"

  log "github.com/Sirupsen/logrus"

  "github.com/xhs/goblice"
  "github.com/xhs/godtls"
  "github.com/xhs/gosctp"
)

func init() {
  log.SetLevel(log.DebugLevel)
}

const (
  roleClient = 0
  roleServer = 1

  stateClosed = 0
  stateConnecting = 1
  stateConnected = 2
)

type Peer struct {
  ice *goblice.Agent
  ctx *godtls.DtlsContext
  dtls *godtls.DtlsTransport
  sctp *gosctp.SctpTransport
  role int
  remotePort int
  state int
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
    state: stateClosed,
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
  if p.state == stateConnecting {
    if err := p.ice.GatherCandidates(); err != nil {
      return err
    }
  }
  go p.ice.Run()

  signalChan := s.ReceiveFrom()
  for {
    select {
    case cand := <-p.ice.CandidateChannel:
      candidate, err := json.Marshal(&Signal{"candidate", cand})
      if err != nil {
        return err
      }
      s.Send(candidate)
      continue
    case e := <-p.ice.EventChannel:
      if e == goblice.EventNegotiationDone {
        break
      }
      continue
    case data := <-signalChan:
      log.Debug("signal received:", string(data))
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
        p.ice.GatherCandidates()
      } else if sig.Type == "answer" {
        p.ParseOfferSdp(sig.Value)
      }
      continue
    }
    break
  }
  log.Debug("ICE negotiation done")

  if p.role == roleClient {
    log.Debug("DTLS connecting")
    p.dtls.SetConnectState()
  } else {
    log.Debug("DTLS accepting")
    p.dtls.SetAcceptState()
  }

  go func () {
    var buf [1 << 16]byte
    for {
      data := <-p.ice.DataChannel
      log.Debug(len(data), " bytes of DTLS data received")
      p.dtls.Feed(data)

      n, _ := p.dtls.Read(buf[:])
      if n > 0 {
        log.Debug(n, " bytes of SCTP data received")
        p.sctp.Feed(buf[:n])
      }
    }
  }()

  exitTick := make(chan bool)
  go func () {
    var buf [1 << 16]byte
    tick := time.Tick(4 * time.Millisecond)
    for {
      select {
      case <-tick:
        n, _ := p.dtls.Spew(buf[:])
        if n > 0 {
          log.Debug(n, " bytes of DTLS data ready")
          rv, err := p.ice.Send(buf[:n])
          if err != nil {
            log.Warn(err)
          } else {
            log.Debug(rv, " bytes of DTLS data sent")
          }
        }
        continue
      case <-exitTick:
        close(exitTick)
        // flush data
        n, _ := p.dtls.Spew(buf[:])
        if n > 0 {
          log.Debug(n, " bytes of DTLS data ready")
          rv, err := p.ice.Send(buf[:n])
          if err != nil {
            log.Warn(err)
          } else {
            log.Debug(rv, " bytes of DTLS data sent")
          }
        }
        break
      }
      break
    }
  }()

  if err := p.dtls.Handshake(); err != nil {
    return err
  }
  exitTick <- true
  log.Debug("DTLS handshake done")

  go func () {
    var buf [1 << 16]byte
    for {
      data := <-p.sctp.BufferChannel
      log.Debug(len(data), " bytes of SCTP data ready")
      p.dtls.Write(data)

      n, _ := p.dtls.Spew(buf[:])
      if n > 0 {
        log.Debug(n, " bytes of DTLS data ready")
        rv, err := p.ice.Send(buf[:n])
        if err != nil {
          log.Warn(err)
        } else {
          log.Debug(rv, " bytes of DTLS data sent")
        }
      }
    }
  }()

  if p.role == roleClient {
    if err := p.sctp.Connect(p.remotePort); err != nil {
      return err
    }
  } else {
    if err := p.sctp.Accept(); err != nil {
      return err
    }
  }
  p.state = stateConnected
  log.Debug("SCTP handshake done")

  for {
    select {
    case d := <-p.sctp.DataChannel:
      log.Debugf("sid: %d, ppid: %d, data: %v", d.Sid, d.Ppid, d.Data)
    }
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
  offer = append(offer, "m=application 1 UDP/DTLS/SCTP webrtc-datachannel")
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
  offer = append(offer, fmt.Sprintf("a=sctp-port:%d", p.sctp.Port))
  offer = append(offer, "")

  p.state = stateConnecting

  return strings.Join(offer, "\r\n"), nil
}

func (p *Peer) ParseOfferSdp(offer string) (int, error) {
  sdps := strings.Split(offer, "\r\n")
  for i := range sdps {
    if strings.HasPrefix(sdps[i], "a=sctp-port:") {
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

  p.state = stateConnecting

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
