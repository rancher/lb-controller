package rancher

import (
	"github.com/fsnotify/fsnotify"
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
	w, _ := fsnotify.NewWatcher()

	certFetcher := &RCertificateFetcher{
		mu:      &sync.RWMutex{},
		Watcher: w,
	}
	testlbc.CertFetcher = certFetcher

	go testlbc.CertFetcher.ProcessFileUpdateEvents(func(string) {})
}

func TestReadCertDir(t *testing.T) {
	labels := make(map[string]string)
	labels["io.rancher.lb_service.cert_dir"] = "./testcerts/certs"

	configs, err := testlbc.BuildConfigFromMetadata("test", "", "", "any", nil, labels)
	if err != nil {
		t.Fatalf("Error building config %v", err)
	}

	if len(configs[0].Certs) != 2 {
		t.Fatalf("Failed to read the certificates from directory %v", labels)
	}
}

func TestReadDefaultCertDir(t *testing.T) {
	labels := make(map[string]string)
	labels["io.rancher.lb_service.default_cert_dir"] = "./testcerts/defaultCert"

	configs, err := testlbc.BuildConfigFromMetadata("test", "", "", "any", nil, labels)
	if err != nil {
		t.Fatalf("Error building config %v", err)
	}

	if configs[0].DefaultCert == nil {
		t.Fatalf("Failed to read the default certificate from directory %v", labels)
	}
}

func TestCheckCertDirUpdate(t *testing.T) {
	labels := make(map[string]string)
	labels["io.rancher.lb_service.cert_dir"] = "testcerts/certs"
	labels["io.rancher.lb_service.default_cert_dir"] = "testcerts/defaultCert"

	configs, err := testlbc.BuildConfigFromMetadata("test", "", "", "any", nil, labels)
	if err != nil {
		t.Fatalf("Error building config %v", err)
	}

	if len(configs[0].Certs) != 3 {
		t.Fatalf("Failed to read the certificates from directory %v", labels)
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

	newConfigs, err2 := testlbc.BuildConfigFromMetadata("test", "", "", "any", nil, labels)
	if err2 != nil {
		t.Fatalf("Error building config %v", err)
	}

	if len(newConfigs[0].Certs) != 4 {
		t.Fatalf("Failed to read the Updated certificates from directory %v", configs[0].Certs)
	}
}
