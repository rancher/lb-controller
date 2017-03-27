package rancher

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/rancher/go-rancher-metadata/metadata"
	"github.com/rancher/go-rancher/v2"
	"github.com/rancher/lb-controller/config"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sync"
	"time"
)

const (
	DefaultCertName = "fullchain.pem"
	DefaultKeyName  = "privkey.pem"
)

type CertificateFetcher interface {
	FetchCertificates(lbMeta *LBMetadata, isDefaultCert bool) ([]*config.Certificate, error)
	UpdateEndpoints(lbSvc *metadata.Service, eps []client.PublicEndpoint) error
	LookForCertUpdates(do func(string))
}

type RCertificateFetcher struct {
	Client *client.RancherClient

	CertDir        string
	DefaultCertDir string

	CertsCache  map[string]*config.Certificate //cert name (sub dir name) -> cert
	DefaultCert *config.Certificate

	tempCertsMap        map[string]*config.Certificate //cert name (sub dir name) -> cert
	updateCheckInterval int
	forceUpdateInterval float64

	mu *sync.RWMutex

	CertName string
	KeyName  string

	initPollDone bool
	initPollMu   *sync.RWMutex
}

func (fetcher *RCertificateFetcher) checkIfInitPollDone() bool {
	isDone := false

	fetcher.initPollMu.RLock()
	isDone = fetcher.initPollDone
	fetcher.initPollMu.RUnlock()

	return isDone
}

func (fetcher *RCertificateFetcher) setInitPollDone() {
	fetcher.initPollMu.Lock()
	fetcher.initPollDone = true
	fetcher.initPollMu.Unlock()
}

func (fetcher *RCertificateFetcher) FetchCertificates(lbMeta *LBMetadata, isDefaultCert bool) ([]*config.Certificate, error) {
	// fetch certificates either from mounted certDir or from cattle
	certs := []*config.Certificate{}
	var defaultCert *config.Certificate

	if fetcher.CertDir != "" || fetcher.DefaultCertDir != "" {
		for {
			if fetcher.checkIfInitPollDone() {
				if isDefaultCert {
					if fetcher.DefaultCertDir != "" {
						logrus.Debugf("Found defaultCertDir label %v", fetcher.DefaultCertDir)
						defaultCert = fetcher.ReadDefaultCertificate(fetcher.DefaultCertDir)
						if defaultCert != nil {
							certs = append(certs, defaultCert)
						}
					}
				} else {
					//read all the certificates from the mounted certDir
					if fetcher.CertDir != "" {
						logrus.Debugf("Found certDir label %v", fetcher.CertDir)
						certsFromDir := fetcher.ReadAllCertificatesFromDir(fetcher.CertDir)
						certs = append(certs, certsFromDir...)
					}
				}
				break
			} else {
				logrus.Debugf("Waiting for InitPollDone()")
				time.Sleep(time.Duration(2) * time.Second)
			}
		}
	} else {
		if !isDefaultCert {
			for _, certName := range lbMeta.Certs {
				cert, err := fetcher.FetchRancherCertificate(certName)
				if err != nil {
					return nil, err
				}
				certs = append(certs, cert)
			}
		} else {
			if lbMeta.DefaultCert != "" {
				var err error
				defaultCert, err = fetcher.FetchRancherCertificate(lbMeta.DefaultCert)
				if err != nil {
					return nil, err
				}

				if defaultCert != nil {
					certs = append(certs, defaultCert)
				}
			}
		}
	}
	return certs, nil
}

func (fetcher *RCertificateFetcher) FetchRancherCertificate(certName string) (*config.Certificate, error) {
	if certName == "" {
		return nil, nil
	}
	opts := client.NewListOpts()
	opts.Filters["name"] = certName
	opts.Filters["removed_null"] = "1"

	certs, err := fetcher.Client.Certificate.List(opts)
	if err != nil {
		return nil, fmt.Errorf("Coudln't get certificate by name [%s]. Error: %#v", certName, err)
	}
	var cert client.Certificate
	var certWithChain string
	if len(certs.Data) >= 1 {
		cert = certs.Data[0]
		certWithChain = fmt.Sprintf("%s\n%s", cert.Cert, cert.CertChain)
	}
	return &config.Certificate{
		Name: cert.Name,
		Key:  cert.Key,
		Cert: certWithChain,
	}, nil
}

func (fetcher *RCertificateFetcher) UpdateEndpoints(lbSvc *metadata.Service, eps []client.PublicEndpoint) error {
	opts := client.NewListOpts()
	opts.Filters["uuid"] = lbSvc.UUID
	opts.Filters["removed_null"] = "1"
	lbs, err := fetcher.Client.LoadBalancerService.List(opts)
	if err != nil {
		return fmt.Errorf("Coudln't get LB service by uuid [%s]. Error: %#v", lbSvc.UUID, err)
	}
	if len(lbs.Data) == 0 {
		logrus.Infof("Failed to find lb by uuid %s", lbSvc.UUID)
		return nil
	}
	lb := lbs.Data[0]

	toUpdate := make(map[string]interface{})
	toUpdate["publicEndpoints"] = eps
	logrus.Infof("Updating Rancher LB [%s] in stack [%s] with the new public endpoints [%v] ", lbSvc.Name, lbSvc.StackName, eps)
	if _, err := fetcher.Client.LoadBalancerService.Update(&lb, toUpdate); err != nil {
		return fmt.Errorf("Failed to update Rancher LB [%s] in stack [%s]. Error: %#v", lbSvc.Name, lbSvc.StackName, err)
	}
	return nil
}

func (fetcher *RCertificateFetcher) ReadAllCertificatesFromDir(certDir string) []*config.Certificate {
	certs := []*config.Certificate{}

	fetcher.mu.RLock()
	for _, value := range fetcher.CertsCache {
		certs = append(certs, value)
	}
	fetcher.mu.RUnlock()

	return certs
}

func (fetcher *RCertificateFetcher) ReadDefaultCertificate(defaultCertDir string) *config.Certificate {
	var currentDefCert *config.Certificate

	fetcher.mu.RLock()
	currentDefCert = fetcher.DefaultCert
	fetcher.mu.RUnlock()

	return currentDefCert
}

func (fetcher *RCertificateFetcher) LookForCertUpdates(doOnUpdate func(string)) {
	lastUpdated := time.Now()
	for {
		logrus.Debugf("Start --- LookForCertUpdates polling dir %v", fetcher.CertDir)
		forceUpdate := false
		certsUpdatedFlag := false
		logrus.Debugf("lastUpdated %v", lastUpdated)

		if time.Since(lastUpdated).Seconds() >= fetcher.forceUpdateInterval {
			logrus.Infof("LookForCertUpdates: Executing force update as certs in cache have not been updated in: %v seconds", fetcher.forceUpdateInterval)
			forceUpdate = true
		}

		//read the certs from the dir into tempMap
		fetcher.tempCertsMap = make(map[string]*config.Certificate)
		err := filepath.Walk(fetcher.CertDir, fetcher.readCertificate)
		if err != nil {
			logrus.Errorf("LookForCertUpdates: Error %v reading certs from cert dir  %v", err, fetcher.CertDir)
		} else {
			//compare with existing cache
			if forceUpdate || !reflect.DeepEqual(fetcher.CertsCache, fetcher.tempCertsMap) {
				if !forceUpdate {
					logrus.Infof("LookForCertUpdates: Found an update in cert dir %v, updating the cache", fetcher.CertDir)
				} else {
					logrus.Infof("LookForCertUpdates: Force Update triggered, updating the cache from cert dir %v", fetcher.CertDir)
				}
				//there is some change, refresh certs
				fetcher.mu.Lock()
				fetcher.CertsCache = make(map[string]*config.Certificate)
				for path, newCert := range fetcher.tempCertsMap {
					fetcher.CertsCache[path] = newCert
					logrus.Debugf("LookForCertUpdates: Cert is reloaded in cache : %v", newCert)
				}
				certsUpdatedFlag = true
				fetcher.mu.Unlock()
			}
		}

		//read the cert from the defaultCertDir into tempMap
		fetcher.tempCertsMap = make(map[string]*config.Certificate)
		err = filepath.Walk(fetcher.DefaultCertDir, fetcher.readCertificate)
		if err != nil {
			logrus.Errorf("LookForCertUpdates: Error %v reading default cert from dir  %v", err, fetcher.DefaultCertDir)
		} else {
			var tempDefCert *config.Certificate
			for _, cert := range fetcher.tempCertsMap {
				tempDefCert = cert
			}
			//compare with existing default cert
			if forceUpdate || !reflect.DeepEqual(fetcher.DefaultCert, tempDefCert) {
				fetcher.mu.Lock()
				fetcher.DefaultCert = tempDefCert
				certsUpdatedFlag = true
				fetcher.mu.Unlock()
			}
		}

		if certsUpdatedFlag {
			//scheduleApplyConfig
			doOnUpdate("")
			lastUpdated = time.Now()
		}

		if !fetcher.checkIfInitPollDone() {
			fetcher.setInitPollDone()
		}

		logrus.Debug("Done --- LookForCertUpdates poll")
		time.Sleep(time.Duration(fetcher.updateCheckInterval) * time.Second)
	}
}

func (fetcher *RCertificateFetcher) readCertificate(path string, f os.FileInfo, err error) error {
	if f != nil && f.IsDir() {
		logrus.Debugf("Walking dir %v", path)
		isCertFound := false
		cert := config.Certificate{}
		cert.Name = f.Name()
		files, err := ioutil.ReadDir(path)
		if err != nil {
			return err
		}
		for _, file := range files {
			if !file.IsDir() {
				contentBytes, err := fetcher.evaluatueLinkAndReadFile(path, file.Name())
				if err != nil {
					return err
				}
				if file.Name() == fetcher.CertName {
					isCertFound = true
					cert.Cert = string(*contentBytes)
				} else if file.Name() == fetcher.KeyName {
					isCertFound = true
					cert.Key = string(*contentBytes)
				}
			}
		}
		if isCertFound {
			fetcher.tempCertsMap[path] = &cert
		}
	}
	return nil
}

func (fetcher *RCertificateFetcher) evaluatueLinkAndReadFile(relativePath string, fileName string) (*[]byte, error) {
	filePath := path.Join(relativePath, fileName)

	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("Error forming path to file %s, error: %v", filePath, err)
	}

	fInfo, err := os.Lstat(absFilePath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("File %s does not exist", absFilePath)
	}

	targetPath := absFilePath
	if fInfo.Mode()&os.ModeSymlink != 0 {
		//read symlink
		targetPath, err := filepath.EvalSymlinks(absFilePath)
		if err != nil {
			return nil, fmt.Errorf("File %s pointed by symlink %s does not exist, error: %v", targetPath, absFilePath, err)
		}
	}
	//read target file
	return fetcher.readFile(targetPath)
}

func (fetcher *RCertificateFetcher) readFile(filePath string) (*[]byte, error) {
	contentBytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("Error reading file %s, error: %v", filePath, err)
	}
	return &contentBytes, nil
}
