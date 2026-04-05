// Package discovery implements ONVIF WS-Discovery and camera probing.
// It finds all ONVIF cameras on the local subnet, authenticates to each,
// and returns a normalized Camera struct with RTSP URLs and metadata.
package discovery

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Camera represents a discovered ONVIF camera with all metadata needed
// to configure recording and streaming.
type Camera struct {
	ID           string // stable UUID derived from MAC or serial
	Name         string // human-readable: manufacturer + model
	Manufacturer string
	Model        string
	Serial       string
	FirmwareVer  string
	IP           string
	ONVIFPort    int
	MainStream   RTSPStream // high-res for recording
	SubStream    RTSPStream // low-res for live dashboard view
	PTZSupported bool
	Profiles     []MediaProfile
	DiscoveredAt time.Time
}

// RTSPStream holds the RTSP URL and stream characteristics.
type RTSPStream struct {
	URL        string
	Width      int
	Height     int
	FPS        int
	Codec      string // H264 or H265
	Bitrate    int    // kbps
}

// MediaProfile is an ONVIF media profile (camera preset stream config).
type MediaProfile struct {
	Token  string
	Name   string
	Width  int
	Height int
}

// Credentials holds authentication for a camera or subnet.
type Credentials struct {
	Username string
	Password string
}

// Discoverer handles ONVIF camera discovery on the local network.
type Discoverer struct {
	creds   []Credentials // try each in order until one works
	timeout time.Duration
}

// New creates a Discoverer. Pass multiple credentials to try against each camera.
func New(creds []Credentials, timeout time.Duration) *Discoverer {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &Discoverer{creds: creds, timeout: timeout}
}

// Discover runs WS-Discovery on the local network and returns all found cameras.
// It probes each discovered device in parallel.
func (d *Discoverer) Discover(ctx context.Context) ([]Camera, error) {
	endpoints, err := wsDiscover(ctx, d.timeout)
	if err != nil {
		return nil, fmt.Errorf("ws-discovery: %w", err)
	}

	var (
		mu      sync.Mutex
		cameras []Camera
		wg      sync.WaitGroup
	)

	for _, ep := range endpoints {
		wg.Add(1)
		go func(endpoint string) {
			defer wg.Done()
			cam, err := d.probeDevice(ctx, endpoint)
			if err != nil {
				// Log and skip — partial failures are normal
				fmt.Printf("probe %s failed: %v\n", endpoint, err)
				return
			}
			mu.Lock()
			cameras = append(cameras, *cam)
			mu.Unlock()
		}(ep)
	}

	wg.Wait()
	return cameras, nil
}

// DiscoverSubnet probes a specific IP range directly, bypassing multicast.
// Useful when multicast is blocked or for scanning a specific VLAN.
func (d *Discoverer) DiscoverSubnet(ctx context.Context, cidr string) ([]Camera, error) {
	ips, err := hostsInCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("parse cidr: %w", err)
	}

	// Common ONVIF ports to try
	onvifPorts := []int{80, 8080, 8000, 2020}

	var (
		mu      sync.Mutex
		cameras []Camera
		wg      sync.WaitGroup
		sem     = make(chan struct{}, 50) // limit concurrency
	)

	for _, ip := range ips {
		for _, port := range onvifPorts {
			wg.Add(1)
			go func(ip string, port int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				endpoint := fmt.Sprintf("http://%s:%d/onvif/device_service", ip, port)
				cam, err := d.probeDevice(ctx, endpoint)
				if err != nil {
					return
				}
				mu.Lock()
				cameras = append(cameras, *cam)
				mu.Unlock()
			}(ip, port)
		}
	}

	wg.Wait()
	return cameras, nil
}

// ─── WS-Discovery ────────────────────────────────────────────────────────────

const (
	wsDiscoveryAddr = "239.255.255.250:3702"
	wsDiscoveryMsg  = `<?xml version="1.0" encoding="UTF-8"?>
<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope"
            xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing"
            xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
            xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
  <e:Header>
    <w:MessageID>uuid:%s</w:MessageID>
    <w:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To>
    <w:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action>
  </e:Header>
  <e:Body>
    <d:Probe>
      <d:Types>dn:NetworkVideoTransmitter</d:Types>
    </d:Probe>
  </e:Body>
</e:Envelope>`
)

// wsDiscoverResponse is the parsed WS-Discovery ProbeMatch response.
type wsDiscoverResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		ProbeMatches struct {
			ProbeMatch []struct {
				XAddrs string `xml:"XAddrs"`
				Types  string `xml:"Types"`
			} `xml:"ProbeMatch"`
		} `xml:"ProbeMatches"`
	} `xml:"Body"`
}

// wsDiscover sends a WS-Discovery multicast probe and collects responses.
func wsDiscover(ctx context.Context, timeout time.Duration) ([]string, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Send probe
	msg := fmt.Sprintf(wsDiscoveryMsg, uuid.New().String())
	dst, _ := net.ResolveUDPAddr("udp4", wsDiscoveryAddr)
	if _, err := conn.WriteToUDP([]byte(msg), dst); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(timeout)
	conn.SetDeadline(deadline)

	seen := map[string]bool{}
	var endpoints []string
	buf := make([]byte, 65536)

	for {
		select {
		case <-ctx.Done():
			return endpoints, nil
		default:
		}

		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break
			}
			break
		}

		var resp wsDiscoverResponse
		if err := xml.Unmarshal(buf[:n], &resp); err != nil {
			continue
		}

		for _, match := range resp.Body.ProbeMatches.ProbeMatch {
			// XAddrs can contain multiple space-separated URLs
			for _, addr := range strings.Fields(match.XAddrs) {
				if !seen[addr] {
					seen[addr] = true
					endpoints = append(endpoints, addr)
				}
			}
		}
	}

	return endpoints, nil
}

// ─── Device probing ───────────────────────────────────────────────────────────

// probeDevice authenticates to an ONVIF endpoint and fetches all camera metadata.
func (d *Discoverer) probeDevice(ctx context.Context, endpoint string) (*Camera, error) {
	// Extract IP and port from endpoint URL
	ip, port, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}

	// Try each set of credentials
	var (
		authedCreds *Credentials
		devInfo     *deviceInfo
	)
	for _, cred := range d.creds {
		c := cred
		info, err := getDeviceInformation(ctx, endpoint, c)
		if err == nil {
			authedCreds = &c
			devInfo = info
			break
		}
	}
	if authedCreds == nil {
		return nil, fmt.Errorf("no valid credentials for %s", endpoint)
	}

	// Get media profiles
	profiles, err := getMediaProfiles(ctx, endpoint, *authedCreds)
	if err != nil {
		return nil, fmt.Errorf("get profiles: %w", err)
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("no media profiles on %s", endpoint)
	}

	// Get RTSP stream URIs for main and sub streams
	mainProfile := profiles[0] // highest resolution first
	mainURI, err := getStreamURI(ctx, endpoint, *authedCreds, mainProfile.Token)
	if err != nil {
		return nil, fmt.Errorf("get main stream URI: %w", err)
	}

	// Inject credentials into RTSP URL
	mainURI = injectRTSPCredentials(mainURI, authedCreds.Username, authedCreds.Password)

	mainStream := RTSPStream{
		URL:    mainURI,
		Width:  mainProfile.Width,
		Height: mainProfile.Height,
		FPS:    15,
		Codec:  "H264",
	}

	subStream := mainStream // fallback: same as main

	// Try to get a sub-stream (second profile at lower resolution)
	if len(profiles) > 1 {
		subProfile := profiles[len(profiles)-1] // lowest resolution
		if subURI, err := getStreamURI(ctx, endpoint, *authedCreds, subProfile.Token); err == nil {
			subURI = injectRTSPCredentials(subURI, authedCreds.Username, authedCreds.Password)
			subStream = RTSPStream{
				URL:    subURI,
				Width:  subProfile.Width,
				Height: subProfile.Height,
				FPS:    10,
				Codec:  "H264",
			}
		}
	}

	// Derive a stable camera ID from serial or IP+model
	stableID := deriveID(devInfo.SerialNumber, ip, devInfo.Model)

	cam := &Camera{
		ID:           stableID,
		Name:         fmt.Sprintf("%s %s", devInfo.Manufacturer, devInfo.Model),
		Manufacturer: devInfo.Manufacturer,
		Model:        devInfo.Model,
		Serial:       devInfo.SerialNumber,
		FirmwareVer:  devInfo.FirmwareVersion,
		IP:           ip,
		ONVIFPort:    port,
		MainStream:   mainStream,
		SubStream:    subStream,
		Profiles:     profiles,
		DiscoveredAt: time.Now(),
	}

	return cam, nil
}

// ─── ONVIF SOAP helpers ───────────────────────────────────────────────────────

type deviceInfo struct {
	Manufacturer    string
	Model           string
	FirmwareVersion string
	SerialNumber    string
	HardwareID      string
}

// getDeviceInformation calls ONVIF GetDeviceInformation.
func getDeviceInformation(ctx context.Context, endpoint string, creds Credentials) (*deviceInfo, error) {
	body := `<tds:GetDeviceInformation xmlns:tds="http://www.onvif.org/ver10/device/wsdl"/>`
	resp, err := soapRequest(ctx, endpoint, "http://www.onvif.org/ver10/device/wsdl/GetDeviceInformation", body, creds)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response struct {
				Manufacturer    string `xml:"Manufacturer"`
				Model           string `xml:"Model"`
				FirmwareVersion string `xml:"FirmwareVersion"`
				SerialNumber    string `xml:"SerialNumber"`
				HardwareId      string `xml:"HardwareId"`
			} `xml:"GetDeviceInformationResponse"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(resp, &parsed); err != nil {
		return nil, err
	}
	r := parsed.Body.Response
	return &deviceInfo{
		Manufacturer:    r.Manufacturer,
		Model:           r.Model,
		FirmwareVersion: r.FirmwareVersion,
		SerialNumber:    r.SerialNumber,
		HardwareID:      r.HardwareId,
	}, nil
}

// getMediaProfiles calls ONVIF GetProfiles and returns sorted profiles (high-res first).
func getMediaProfiles(ctx context.Context, endpoint string, creds Credentials) ([]MediaProfile, error) {
	// Media service endpoint is typically /onvif/media_service
	mediaEndpoint := strings.Replace(endpoint, "device_service", "media_service", 1)
	if mediaEndpoint == endpoint {
		mediaEndpoint = strings.Replace(endpoint, "/onvif/device", "/onvif/media", 1)
	}

	body := `<trt:GetProfiles xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/>`
	resp, err := soapRequest(ctx, mediaEndpoint, "http://www.onvif.org/ver10/media/wsdl/GetProfiles", body, creds)
	if err != nil {
		// Fall back to device endpoint
		resp, err = soapRequest(ctx, endpoint, "http://www.onvif.org/ver10/media/wsdl/GetProfiles", body, creds)
		if err != nil {
			return nil, err
		}
	}

	var parsed struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response struct {
				Profiles []struct {
					Token         string `xml:"token,attr"`
					Name          string `xml:"Name"`
					VideoEncoder  struct {
						Resolution struct {
							Width  int `xml:"Width"`
							Height int `xml:"Height"`
						} `xml:"Resolution"`
						RateControl struct {
							FrameRateLimit int `xml:"FrameRateLimit"`
						} `xml:"RateControl"`
					} `xml:"VideoEncoderConfiguration"`
				} `xml:"Profiles"`
			} `xml:"GetProfilesResponse"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(resp, &parsed); err != nil {
		return nil, err
	}

	var profiles []MediaProfile
	for _, p := range parsed.Body.Response.Profiles {
		profiles = append(profiles, MediaProfile{
			Token:  p.Token,
			Name:   p.Name,
			Width:  p.VideoEncoder.Resolution.Width,
			Height: p.VideoEncoder.Resolution.Height,
		})
	}

	// Sort highest resolution first
	for i := 0; i < len(profiles); i++ {
		for j := i + 1; j < len(profiles); j++ {
			if profiles[j].Width*profiles[j].Height > profiles[i].Width*profiles[i].Height {
				profiles[i], profiles[j] = profiles[j], profiles[i]
			}
		}
	}

	return profiles, nil
}

// getStreamURI calls ONVIF GetStreamUri for a given profile token.
func getStreamURI(ctx context.Context, endpoint string, creds Credentials, profileToken string) (string, error) {
	mediaEndpoint := strings.Replace(endpoint, "device_service", "media_service", 1)

	body := fmt.Sprintf(`<trt:GetStreamUri xmlns:trt="http://www.onvif.org/ver10/media/wsdl">
		<trt:StreamSetup>
			<tt:Stream xmlns:tt="http://www.onvif.org/ver10/schema">RTP-Unicast</tt:Stream>
			<tt:Transport xmlns:tt="http://www.onvif.org/ver10/schema">
				<tt:Protocol>RTSP</tt:Protocol>
			</tt:Transport>
		</trt:StreamSetup>
		<trt:ProfileToken>%s</trt:ProfileToken>
	</trt:GetStreamUri>`, profileToken)

	resp, err := soapRequest(ctx, mediaEndpoint, "http://www.onvif.org/ver10/media/wsdl/GetStreamUri", body, creds)
	if err != nil {
		resp, err = soapRequest(ctx, endpoint, "http://www.onvif.org/ver10/media/wsdl/GetStreamUri", body, creds)
		if err != nil {
			return "", err
		}
	}

	var parsed struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response struct {
				MediaUri struct {
					Uri string `xml:"Uri"`
				} `xml:"MediaUri"`
			} `xml:"GetStreamUriResponse"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(resp, &parsed); err != nil {
		return "", err
	}

	uri := parsed.Body.Response.MediaUri.Uri
	if uri == "" {
		return "", fmt.Errorf("empty stream URI")
	}
	return uri, nil
}

// ─── SOAP transport ───────────────────────────────────────────────────────────

// soapRequest sends a SOAP request with WS-UsernameToken authentication.
func soapRequest(ctx context.Context, endpoint, action, body string, creds Credentials) ([]byte, error) {
	nonce := make([]byte, 16)
	rand.Read(nonce)
	nonce64 := base64.StdEncoding.EncodeToString(nonce)

	created := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// WS-Security PasswordDigest: Base64(SHA1(nonce + created + password))
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(creds.Password))
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))

	envelope := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
            xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">
  <s:Header>
    <wsse:Security>
      <wsse:UsernameToken>
        <wsse:Username>%s</wsse:Username>
        <wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">%s</wsse:Password>
        <wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">%s</wsse:Nonce>
        <wsu:Created>%s</wsu:Created>
      </wsse:UsernameToken>
    </wsse:Security>
  </s:Header>
  <s:Body>%s</s:Body>
</s:Envelope>`, creds.Username, digest, nonce64, created, body)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(envelope))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	req.Header.Set("SOAPAction", action)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	buf := make([]byte, 1<<20) // 1MB max
	n, _ := resp.Body.Read(buf)
	return buf[:n], nil
}

// ─── Utilities ────────────────────────────────────────────────────────────────

// injectRTSPCredentials adds credentials into rtsp://host → rtsp://user:pass@host
func injectRTSPCredentials(rawURL, username, password string) string {
	if username == "" {
		return rawURL
	}
	if strings.HasPrefix(rawURL, "rtsp://") {
		rest := strings.TrimPrefix(rawURL, "rtsp://")
		// Remove existing credentials if present
		if at := strings.Index(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		return fmt.Sprintf("rtsp://%s:%s@%s", username, password, rest)
	}
	return rawURL
}

// deriveID creates a stable UUID from camera serial or IP+model.
func deriveID(serial, ip, model string) string {
	seed := serial
	if seed == "" {
		seed = ip + model
	}
	// Use UUID v5 (SHA-1 namespace) for stable deterministic IDs
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(seed)).String()
}

// parseEndpoint extracts IP and port from an ONVIF endpoint URL.
func parseEndpoint(endpoint string) (string, int, error) {
	s := strings.TrimPrefix(endpoint, "http://")
	s = strings.TrimPrefix(s, "https://")
	hostport := strings.Split(s, "/")[0]

	host, portStr, err := net.SplitHostPort(hostport)
	if err != nil {
		// No port in URL, default to 80
		return hostport, 80, nil
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return host, port, nil
}

// hostsInCIDR returns all host IPs in a CIDR range (excludes network + broadcast).
func hostsInCIDR(cidr string) ([]string, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	var ips []string
	for ip := cloneIP(ipNet.IP.Mask(ipNet.Mask)); ipNet.Contains(ip); incrementIP(ip) {
		host := ip.String()
		// Skip network address and broadcast
		if host == ipNet.IP.String() {
			continue
		}
		ips = append(ips, host)
	}
	// Remove broadcast (last entry)
	if len(ips) > 0 {
		ips = ips[:len(ips)-1]
	}
	return ips, nil
}

func cloneIP(ip net.IP) net.IP {
	c := make(net.IP, len(ip))
	copy(c, ip)
	return c
}

func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}
