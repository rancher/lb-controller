package rancher

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/fsnotify/fsnotify"
	"github.com/rancher/go-rancher-metadata/metadata"
	"github.com/rancher/go-rancher/v2"
	"github.com/rancher/lb-controller/config"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

const (
	CertName = "fullchain.pem"
	KeyName  = "privkey.pem"
)

type CertificateFetcher interface {
	FetchCertificate(certName string) (*config.Certificate, error)
	UpdateEndpoints(lbSvc *metadata.Service, eps []client.PublicEndpoint) error
	ReadAllCertificatesFromDir(certDir string) ([]*config.Certificate, error)
	ReadDefaultCertificate(defaultCertDir string) (*config.Certificate, error)
	StopWatcher() error
	ProcessFileUpdateEvents(do func(string))
}

type RCertificateFetcher struct {
	Client         *client.RancherClient
	CertsCache     map[string]*config.Certificate //cert name (sub dir name) -> cert
	CertDir        string
	DefaultCertDir string
	DefaultCert    *config.Certificate
	Watcher        *fsnotify.Watcher
	mu             *sync.RWMutex
	TempCertsMap   map[string]*config.Certificate //cert name (sub dir name) -> cert
}

func (fetcher *RCertificateFetcher) FetchCertificate(certName string) (*config.Certificate, error) {
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

/* Cache the certs present under CertDir and DefaultCertDir
   When the Cache is null, read from the dir and init the cache. When cache is not empty read from the cache.
   Clear cache when the directories change
   Start a Watcher thread when initialising the cache. For every certName, add to watcher.
   When watcher receives event, read that cert and update cache.
   Add a watcher for CertDir/DefaultCertDir also - upon events on these dirs clear the cache and enque.*/

func (fetcher *RCertificateFetcher) ReadAllCertificatesFromDir(certDir string) ([]*config.Certificate, error) {
	if _, err := os.Stat(certDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("Certificate Directory does not exist %v", certDir)
	}
	certs := []*config.Certificate{}

	fetcher.mu.RLock()
	currentCertD := fetcher.CertDir
	fetcher.mu.RUnlock()

	if currentCertD != "" && currentCertD == certDir && fetcher.CertsCache != nil && len(fetcher.CertsCache) > 0 {
		//read from cache if certDir matches and certs are cached.
		logrus.Debug("Reading certs from cache")
	} else {
		// init/re-init cache
		fetcher.mu.Lock()
		fetcher.removeWatches()

		fetcher.CertsCache = make(map[string]*config.Certificate)
		logrus.Debug("Reading certs from directory and starting watchers")
		fetcher.TempCertsMap = make(map[string]*config.Certificate)
		err := filepath.Walk(certDir, fetcher.readCertificate)
		if err != nil {
			fetcher.CertDir = ""
			fetcher.CertsCache = nil
			logrus.Errorf("Error %v reading certs from directory %v", err, certDir)
		} else {
			fetcher.CertDir = certDir
			for path, cert := range fetcher.TempCertsMap {
				fetcher.CertsCache[path] = cert
			}
			fetcher.addWatches()
		}
		fetcher.mu.Unlock()
	}

	fetcher.mu.RLock()
	for _, value := range fetcher.CertsCache {
		certs = append(certs, value)
	}
	fetcher.mu.RUnlock()

	return certs, nil
}

func (fetcher *RCertificateFetcher) ReadDefaultCertificate(defaultCertDir string) (*config.Certificate, error) {
	fetcher.mu.RLock()
	currentDefCertD := fetcher.DefaultCertDir
	fetcher.mu.RUnlock()

	if currentDefCertD != defaultCertDir {
		//defaultCertDir value changed, read it again.
		fetcher.Watcher.Remove(defaultCertDir)
		fetcher.mu.Lock()
		defer fetcher.mu.Unlock()
		fetcher.TempCertsMap = make(map[string]*config.Certificate)
		err := filepath.Walk(defaultCertDir, fetcher.readCertificate)
		if err != nil {
			fetcher.DefaultCertDir = ""
			fetcher.DefaultCert = nil
			logrus.Errorf("Error %v reading defaultCertDir  %v", err, defaultCertDir)
			return fetcher.DefaultCert, err
		}
		fetcher.DefaultCertDir = defaultCertDir
		for _, cert := range fetcher.TempCertsMap {
			fetcher.DefaultCert = cert
		}
		fetcher.Watcher.Add(defaultCertDir)
	}
	return fetcher.DefaultCert, nil
}

func (fetcher *RCertificateFetcher) readCertificate(path string, f os.FileInfo, err error) error {
	if f != nil && f.IsDir() {
		logrus.Debugf("walking %v", path)
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
				if file.Name() == CertName {
					isCertFound = true
					cert.Cert = string(*contentBytes)
				} else if file.Name() == KeyName {
					isCertFound = true
					cert.Key = string(*contentBytes)
				}
			}
		}
		if isCertFound {
			fetcher.TempCertsMap[path] = &cert
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

func (fetcher *RCertificateFetcher) addWatches() {
	if fetcher.CertDir != "" {
		err := fetcher.Watcher.Add(fetcher.CertDir)
		if err != nil {
			logrus.Errorf("Failed to add watch for Dir %v", fetcher.CertDir)
		}
	}

	for dirName := range fetcher.CertsCache {
		err := fetcher.Watcher.Add(dirName)
		if err != nil {
			logrus.Errorf("Failed to add watch for Dir %v", err)
		}
	}
}

func (fetcher *RCertificateFetcher) removeWatches() {
	if fetcher.CertDir != "" {
		err := fetcher.Watcher.Remove(fetcher.CertDir)
		if err != nil {
			logrus.Errorf("Failed to remove watch for Dir %v", fetcher.CertDir)
		}
	}

	for dirName := range fetcher.CertsCache {
		err := fetcher.Watcher.Remove(dirName)
		if err != nil {
			logrus.Errorf("Failed to remove watch from Dir %v", err)
		}
	}
}

func (fetcher *RCertificateFetcher) ProcessFileUpdateEvents(doOnEvent func(string)) {
	for {
		logrus.Debugf("Polling for fsnotify events:")
		select {
		case event := <-fetcher.Watcher.Events:
			logrus.Debugf("event: %v", event)
			if strings.Contains(event.Name, CertName) || strings.Contains(event.Name, KeyName) {
				if event.Op&fsnotify.Create == fsnotify.Create {
					logrus.Debugf("Processing event for file: %v", event.Name)
					lastIndexOfSlash := strings.LastIndex(event.Name, "/")
					if lastIndexOfSlash != -1 {
						certDirPath := event.Name[0:lastIndexOfSlash]
						//reload this cert in cache
						fetcher.mu.Lock()
						fetcher.TempCertsMap = make(map[string]*config.Certificate)
						err := filepath.Walk(certDirPath, fetcher.readCertificate)
						if err != nil {
							logrus.Errorf("Error %v reading certDirPath  %v", err, certDirPath)
						} else {
							for path, newCert := range fetcher.TempCertsMap {
								fetcher.CertsCache[path] = newCert
								logrus.Debugf("Cert is reloaded in cache : %v", newCert)
							}
						}
						fetcher.mu.Unlock()
					}
					//scheduleApplyConfig
					doOnEvent("")
				}
			} else if strings.Contains(event.Name, fetcher.CertDir) || strings.Contains(event.Name, fetcher.DefaultCertDir) {
				//invalidate cache and reload
				logrus.Debugf("Processing event for directory: %v", event.Name)
				fetcher.mu.Lock()
				fetcher.CertDir = ""
				fetcher.DefaultCertDir = ""
				fetcher.mu.Unlock()
				//scheduleApplyConfig
				doOnEvent("")
			}
		case err := <-fetcher.Watcher.Errors:
			logrus.Errorf("ProcessFileUpdateEvents received error: %v", err)
		}
	}
}

func (fetcher *RCertificateFetcher) StopWatcher() error {
	if fetcher.Watcher != nil {
		return fetcher.Watcher.Close()
	}
	return nil
}
