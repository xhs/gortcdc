package gortcdc_test

import (
  "testing"
  "log"
  "encoding/json"

  "github.com/xhs/gortcdc"
)

func TestGenerateOfferSdp(t *testing.T) {
  peer, err := gortcdc.NewPeer()
  if err != nil {
    t.Error(err)
  }
  defer peer.Destroy()

  offer, err := peer.GenerateOfferSdp()
  if err != nil {
    t.Error(err)
  }

  log.Print(offer)
}

func TestParseOfferSdp(t *testing.T) {
  peer, _ := gortcdc.NewPeer()
  defer peer.Destroy()

  offer, _ := peer.GenerateOfferSdp()
  log.Print(offer)

  rv, err := peer.ParseOfferSdp(offer)
  if err != nil {
    t.Error(err)
  }
  if rv != 0 {
    t.Error("unexpected rv:", rv)
  }

  newOffer, _ := peer.GenerateOfferSdp()
  log.Print(newOffer)
}

type dummySignaller struct {
  localCh, remoteCh chan []byte
}

func newDummySignaller(localCh, remoteCh chan []byte) (*dummySignaller) {
  return &dummySignaller{localCh, remoteCh}
}

func (d *dummySignaller) Send(data []byte) error {
  d.remoteCh <- data
  return nil
}

func (d *dummySignaller) ReceiveFrom() <-chan []byte {
  return d.localCh
}

func TestSignalling(t *testing.T) {
  clientCh := make(chan []byte, 16)
  serverCh := make(chan []byte, 16)
  clientSignaller := newDummySignaller(clientCh, serverCh)
  serverSignaller := newDummySignaller(serverCh, clientCh)

  serverPeer, err := gortcdc.NewPeer()
  if err != nil {
    t.Error(err)
  }
  go serverPeer.Run(serverSignaller)

  clientPeer, err := gortcdc.NewPeer()
  if err != nil {
    t.Error(err)
  }

  sdp, err := clientPeer.GenerateOfferSdp()
  if err != nil {
    t.Error(err)
  }
  offer, err := json.Marshal(&gortcdc.Signal{"offer", sdp})
  clientSignaller.Send(offer)

  clientPeer.Run(clientSignaller)
}
