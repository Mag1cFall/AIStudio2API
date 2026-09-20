//go:build !windows || !amd64

package chromeauth

import "fmt"

func retrieveV20Key(string) ([]byte, error) {
	return nil, fmt.Errorf("automatically retrieving Chrome App-Bound master key is only supported on Windows amd64")
}
