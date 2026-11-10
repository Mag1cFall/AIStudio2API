//go:build conservative

package chromeauth

import "errors"

const Supported = false

var errConservative = errors.New("this trial requires login in its own browser; Chrome profile/key extraction and OAuth recovery are disabled")

func DefaultChromeRoot() (string, error) { return "", errConservative }
func ensurePlatformImport() error        { return errConservative }
func Discover(string) ([]Account, error) { return nil, errConservative }
func readTokenService(string, string, string) (string, []byte, []byte, error) {
	return "", nil, nil, errConservative
}
func retrieveV20Key(string) ([]byte, error)                 { return nil, errConservative }
func openDeviceBindingKey([]byte) (deviceBindingKey, error) { return nil, errConservative }
