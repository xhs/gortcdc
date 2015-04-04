package gortcdc_test

import (
  "testing"
  "log"

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
