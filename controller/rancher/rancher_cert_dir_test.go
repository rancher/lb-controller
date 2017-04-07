package rancher

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var testlbc *LoadBalancerController

func init() {
	testlbc = &LoadBalancerController{
		stopCh:                     make(chan struct{}),
		incrementalBackoff:         0,
		incrementalBackoffInterval: 5,
		MetaFetcher:                tMetaFetcher{},
		LBProvider:                 &tProvider{},
	}

	certFetcher := &RCertificateFetcher{
		mu:                  &sync.RWMutex{},
		updateCheckInterval: 5,
		forceUpdateInterval: 15,
		CertDir:             "testcerts/certs",
		DefaultCertDir:      "testcerts/defaultCert",
		CertName:            "fullchain.pem",
		KeyName:             "privkey.pem",
		initPollMu:          &sync.RWMutex{},
	}
	testlbc.CertFetcher = certFetcher

	go testlbc.CertFetcher.LookForCertUpdates(func(string) {})

	time.Sleep(10 * time.Second)
}

func TestReadCertDirs(t *testing.T) {
	configs, err := testlbc.BuildConfigFromMetadata("test", "", "", "any", nil)

	if err != nil {
		t.Fatalf("Error building config %v", err)
	}

	if len(configs[0].Certs) != 3 {
		t.Fatalf("Failed to read the certificates from the directory")
	}
}

func TestReadDefaultCertDir(t *testing.T) {
	configs, err := testlbc.BuildConfigFromMetadata("test", "", "", "any", nil)
	if err != nil {
		t.Fatalf("Error building config %v", err)
	}

	if configs[0].DefaultCert == nil {
		t.Fatalf("Failed to read the default certificate from the directory")
	}
}

func TestCheckCertDirUpdate(t *testing.T) {
	configs, err := testlbc.BuildConfigFromMetadata("test", "", "", "any", nil)
	if err != nil {
		t.Fatalf("Error building config %v", err)
	}

	if len(configs[0].Certs) != 3 {
		t.Fatalf("Failed to read the certificates from the directory")
	}

	path := filepath.Join("./testcerts/certs", "c.com")
	os.MkdirAll(path, os.ModePerm)
	defer os.RemoveAll(path)

	d1 := []byte("some\ncontent\n")
	err = ioutil.WriteFile("./testcerts/certs/c.com/fullchain.pem", d1, 0644)
	if err != nil {
		t.Fatalf("Error writing file %v", err)
	}

	time.Sleep(10 * time.Second)

	newConfigs, err2 := testlbc.BuildConfigFromMetadata("test", "", "", "any", nil)
	if err2 != nil {
		t.Fatalf("Error building config %v", err)
	}

	if len(newConfigs[0].Certs) != 3 {
		t.Fatalf("Failed to read the Updated certificates from the directory")
	}

	err = ioutil.WriteFile("./testcerts/certs/c.com/privkey.pem", d1, 0644)
	if err != nil {
		t.Fatalf("Error writing file %v", err)
	}

	time.Sleep(10 * time.Second)

	newConfigs, err2 = testlbc.BuildConfigFromMetadata("test", "", "", "any", nil)
	if err2 != nil {
		t.Fatalf("Error building config %v", err)
	}

	if len(newConfigs[0].Certs) != 4 {
		t.Fatalf("Failed to read the Updated certificates from the directory")
	}
}
