package main

import "testing"

func TestParsePodList(t *testing.T) {
	body := []byte(`{"items":[
	  {"metadata":{"name":"mg-stub-ready","labels":{"game":"stub","alloc":"false"}},
	   "status":{"podIP":"10.0.0.5","conditions":[{"type":"Ready","status":"True"}]}},
	  {"metadata":{"name":"mg-stub-booting","labels":{"game":"stub","alloc":"false"}},
	   "status":{"conditions":[{"type":"Ready","status":"False"}]}},
	  {"metadata":{"name":"mg-stub-taken","labels":{"game":"stub","alloc":"true"}},
	   "status":{"podIP":"10.0.0.6","conditions":[{"type":"Ready","status":"True"}]}}
	]}`)
	pods, err := parsePodList(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 3 {
		t.Fatalf("got %d pods", len(pods))
	}
	if !pods[0].Ready || pods[0].Alloc || pods[0].IP != "10.0.0.5" || pods[0].Game != "stub" {
		t.Errorf("ready pod parsed wrong: %+v", pods[0])
	}
	if pods[1].Ready {
		t.Errorf("booting pod should not be Ready: %+v", pods[1])
	}
	if !pods[2].Alloc {
		t.Errorf("taken pod should be Alloc: %+v", pods[2])
	}
}
