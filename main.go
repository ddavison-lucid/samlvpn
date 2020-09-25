package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/pkg/errors"
)

func main() {
	log.Println("resolving VPN hostname")
	vpnRemote, err := vpnIPAddress(vpnHost)
	if err != nil {
		log.Fatal(errors.Wrap(err, "could not resolve VPN hostname"))
	}

	log.Println("obtaining AUTH_FAILED response")
	output, err := samlAuthErrorLogOutput(vpnRemote)
	if err != nil {
		log.Fatal(errors.Wrap(err, "could not get AUTH_FAILED response"))
	}

	log.Println("parsing AUTH_FAILED response")
	URL, SID, err := parseOutput(output)
	if err != nil {
		log.Fatal(errors.Wrap(err, "could not parse challenge URL"))
	}

	log.Printf("starting HTTP server on %s, timeout %v", serverAddress, timeout)
	server := NewServer(serverAddress, timeout)
	server.Start()

	if browserCommand == "" {
		log.Println("open this:", URL)
	} else {
		cmd := exec.Command(browserCommand, URL.String())
		log.Println("launching", cmd)
		output := &bytes.Buffer{}
		cmd.Stderr = output
		cmd.Stdout = output
		if err := cmd.Run(); err != nil {
			log.Println(errors.Wrap(err, "could not open URL in browser"))
			log.Println("open this manually:", URL.String())
		}
		log.Println("your browser said:", strings.TrimSpace(output.String()))
	}

	log.Println("waiting for server to receive SAML callback")
	response, err := server.WaitForResponse()
	if err != nil {
		log.Println(errors.Wrap(err, "could not get response"))
	}

	realCredsFile, _, err := tmpfile(fmt.Sprintf(
		"N/A\nCRV1::%s::%s", SID, response))
	if err != nil {
		log.Fatal(errors.Wrap(err, "could not create real credential file"))
	}

	cmd := exec.Command(
		"sudo",
		openvpn,
		"--config", vpnConfig,
		"--verb", "3",
		"--auth-nocache",
		"--inactive", "3600",
		"--proto", vpnProto,
		"--remote", vpnRemote, fmt.Sprint(vpnPort),
		"--script-security", "2",
		"--route-up", fmt.Sprintf("'/bin/rm %s'", realCredsFile),
		"--auth-user-pass", realCredsFile,
	)

	if runCommand {
		cmd.Run()
	} else {
		fmt.Print(cmd.String())
	}
}

func samlAuthErrorLogOutput(vpnRemote string) (string, error) {
	bogusCredsFile, bogusCleanup, err := tmpfile("N/A\nACS::35001")
	if err != nil {
		return "", err
	}
	defer bogusCleanup()

	cmd := exec.Command(
		openvpn,
		"--config", vpnConfig,
		"--verb", "3",
		"--proto", vpnProto,
		"--remote", vpnRemote, fmt.Sprint(vpnPort),
		"--auth-retry", "none",
		"--auth-user-pass", bogusCredsFile,
	)
	output := &bytes.Buffer{}
	cmd.Stdout = output
	cmd.Stderr = output
	_ = cmd.Run()

	return output.String(), nil
}

func vpnIPAddress(hostname string) (string, error) {
	addrs, err := net.LookupHost("randomhostname." + hostname)
	if err != nil {
		return "", errors.Wrap(err, "could not lookup host")
	}
	if len(addrs) < 1 {
		return "", fmt.Errorf("could not lookup host: no addresses found")
	}
	return addrs[0], nil
}

func tmpfile(contents string) (string, func(), error) {
	file, err := ioutil.TempFile("", "openvpn-saml")
	if err != nil {
		return "", nil, errors.Wrap(err, "could not create credentials temp file")
	}
	if _, err := fmt.Fprintf(file, contents); err != nil {
		return "", nil, errors.Wrap(err, "could not write credentials temp file")
	}
	if err := file.Sync(); err != nil {
		return "", nil, errors.Wrap(err, "could not flush credentials temp file")
	}

	cleanup := func() {
		if err := os.Remove(file.Name()); err != nil {
			log.Fatalf("could not remove credentials temp file %q: %s", file.Name(), err)
		}
	}

	return file.Name(), cleanup, nil
}

// parseOutput crudely gets the SAML URL and the SID from the logs output.
func parseOutput(output string) (*url.URL, string, error) {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "AUTH_FAILED") {
			fmt.Println(line)
			split := strings.Split(line, ":")
			if len(split) < 10 {
				return nil, "", fmt.Errorf("could not find SID in output")
			}
			url, err := url.Parse(split[8] + ":" + split[9])
			if err != nil {
				return nil, "", errors.Wrap(err, "could not parse URL")
			}
			return url, split[6], nil
		}
	}

	return nil, "", fmt.Errorf("could not find AUTH_FAILED line")
}