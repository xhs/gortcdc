package gortcdc

import (
  "time"
  "math/rand"
  "fmt"
  "strings"
  "strconv"

  "github.com/xhs/goblice"
  "github.com/xhs/godtls"
  "github.com/xhs/gosctp"
)

const (
  roleClient = 0
  roleServer = 1
)

var numbers = []rune("0123456789")

func randSession() string {
  s := make([]rune, 16)
  rand.Seed(time.Now().UnixNano())
  for i := range s {
    s[i] = numbers[rand.Intn(10)]
  }
  return string(s)
}

type transport struct {
  ice *goblice.Agent
  ctx *godtls.DtlsContext
  dtls *godtls.DtlsTransport
  sctp *gosctp.SctpTransport
  role int
  remotePort int
}

func (p *Peer) newTransport() (*transport, error) {
  ice, err := goblice.NewReliableAgent()
  if err != nil {
    return nil, err
  }

  ice.SetControllingMode(true)
  if p.stun_ip != "" {
    ice.SetStunServer(p.stun_ip)
  }
  ice.SetStunServerPort(p.stun_port)

  stream, err := ice.AddStream(1)
  if err != nil {
    ice.Destroy()
    return nil, err
  }
  if err := ice.SetStreamName(stream, "application"); err != nil {
    ice.Destroy()
    return nil, err
  }

  ctx, err := godtls.NewContext("gortcdc", 3650)
  if err != nil {
    ice.Destroy()
    return nil, err
  }

  dtls, err := ctx.NewTransport()
  if err != nil {
    ctx.Destroy()
    ice.Destroy()
    return nil, err
  }

  rand.Seed(time.Now().UnixNano())
  sctp, err := gosctp.NewTransport(rand.Intn(50001) + 10000)
  if err != nil {
    dtls.Destroy()
    ctx.Destroy()
    ice.Destroy()
    return nil, err
  }

  return &transport{
    ice: ice,
    ctx: ctx,
    dtls: dtls,
    sctp: sctp,
    role: roleClient,
  }, nil
}

func (t *transport) destroy() {
  t.sctp.Destroy()
  t.dtls.Destroy()
  t.ctx.Destroy()
  t.ice.Destroy()
}

func (t *transport) generateOfferSdp() (string, error) {
  var offer []string
  offer = append(offer, "v=0")
  offer = append(offer, fmt.Sprintf("o=- %s 2 IN IP4 127.0.0.1", randSession()))
  offer = append(offer, "s=-")
  offer = append(offer, "t=0 0")
  offer = append(offer, "a=msid-semantic: WMS")
  offer = append(offer, fmt.Sprintf("m=application 1 DTLS/SCTP %d", t.sctp.Port))
  offer = append(offer, "c=IN IP4 0.0.0.0")

  sdp := t.ice.GenerateSdp()
  sdps := strings.Split(sdp, "\n")
  for i := range sdps {
    if strings.HasPrefix(sdps[i], "a=ice-ufrag:") {
      offer = append(offer, sdps[i])
    } else if strings.HasPrefix(sdps[i], "a=ice-pwd:") {
      offer = append(offer, sdps[i])
    }
  }

  offer = append(offer, fmt.Sprintf("a=fingerprint:sha-256 %s", t.dtls.Fingerprint))
  if t.role == roleClient {
    offer = append(offer, "a=setup:active")
  } else {
    offer = append(offer, "a=setup:passive")
  }
  offer = append(offer, "a=mid:data")
  offer = append(offer, fmt.Sprintf("a=sctpmap:%d webrtc-datachannel 1024", t.sctp.Port))

  offer = append(offer, "")
  return strings.Join(offer, "\r\n"), nil
}

func (t *transport) parseOfferSdp(offer string) error {
  sdps := strings.Split(offer, "\r\n")
  for i := range sdps {
    if strings.HasPrefix(sdps[i], "a=sctpmap:") {
      sctpmap := strings.Split(sdps[i], " ")[0]
      port, err := strconv.Atoi(strings.Split(sctpmap, ":")[1])
      if err != nil {
        return err
      }
      t.remotePort = port
    } else if strings.HasPrefix(sdps[i], "a=setup:active") {
      if t.role == roleClient {
        t.role = roleServer
      }
    } else if strings.HasPrefix(sdps[i], "a=setup:passive") {
      if t.role == roleServer {
        t.role = roleClient
      }
    }
  }

  _, err := t.ice.ParseSdp(strings.Join(sdps, "\n"))
  if err != nil {
    return err
  }
  return nil
}
