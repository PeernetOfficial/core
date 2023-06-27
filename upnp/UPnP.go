/*
File Username:  UPnP.go
Copyright:  2021 Peernet s.r.o.
Author:     Peter Kleissner
*/

package upnp

import (
	"bytes"
	"encoding/xml"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// NAT is an interface representing a NAT traversal options for example UPNP or NAT-PMP.
// It provides methods to query and manipulate this traversal to allow access to services.
type NAT interface {
	// Get the external address from outside the NAT.
	GetExternalAddress() (addr net.IP, err error)
	// Add a port mapping for protocol ("udp" or "tcp") from external port to internal port with description lasting for timeout.
	AddPortMapping(protocol string, internalIP net.IP, internalPort, externalPort uint16, description string, timeout int) (mappedExternalPort uint16, err error)
	// Remove a previously added port mapping from external port to internal port.
	DeletePortMapping(protocol string, externalPort uint16) (err error)
}

type upnpNAT struct {
	serviceURL string
	urnDomain  string
	localIP    net.IP
}

// Discover searches the local network for a UPnP router returning a NAT for the network if so, nil if not.
// Socket must be an active local socket.
func Discover(localIP net.IP) (nat NAT, err error) {
	ssdp, err := net.ResolveUDPAddr("udp4", "239.255.255.250:1900")
	if err != nil {
		return
	}
	conn, err := net.ListenPacket("udp4", net.JoinHostPort(localIP.String(), "0")) // use a random port
	if err != nil {
		return
	}
	socket := conn.(*net.UDPConn)
	defer socket.Close()

	err = socket.SetDeadline(time.Now().Add(3 * time.Second))
	if err != nil {
		return
	}

	st := "InternetGatewayDevice:1"

	buf := bytes.NewBufferString(
		"M-SEARCH * HTTP/1.1\r\n" +
			"HOST: 239.255.255.250:1900\r\n" +
			"ST: ssdp:all\r\n" +
			"MAN: \"ssdp:discover\"\r\n" +
			"MX: 2\r\n\r\n")
	message := buf.Bytes()
	answerBytes := make([]byte, 1024)
	for i := 0; i < 3; i++ {
		_, err = socket.WriteToUDP(message, ssdp)
		if err != nil {
			return
		}
		var n int
		_, _, err = socket.ReadFromUDP(answerBytes)
		if err != nil {
			return
		}
		for {
			n, _, err = socket.ReadFromUDP(answerBytes)
			if err != nil {
				break
			}
			answer := string(answerBytes[0:n])
			if !strings.Contains(answer, st) {
				continue
			}
			// HTTP header field names are case-insensitive.
			// http://www.w3.org/Protocols/rfc2616/rfc2616-sec4.html#sec4.2
			locString := "\r\nlocation:"
			answer = strings.ToLower(answer)
			locIndex := strings.Index(answer, locString)
			if locIndex < 0 {
				continue
			}
			loc := answer[locIndex+len(locString):]
			endIndex := strings.Index(loc, "\r\n")
			if endIndex < 0 {
				continue
			}
			locURL := strings.TrimSpace(loc[0:endIndex])
			var serviceURL, urnDomain string
			serviceURL, urnDomain, err = getServiceURL(localIP, locURL)
			if err != nil {
				return
			}
			nat = &upnpNAT{serviceURL: serviceURL, urnDomain: urnDomain, localIP: localIP}
			return
		}
	}
	err = errors.New("UPnP port discovery failed")
	return
}

// service represents the Service type in an UPnP xml description.
// Only the parts we care about are present and thus the xml may have more
// fields than present in the structure.
type service struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

// deviceList represents the deviceList type in an UPnP xml description.
// Only the parts we care about are present and thus the xml may have more
// fields than present in the structure.
type deviceList struct {
	XMLName xml.Name `xml:"deviceList"`
	Device  []device `xml:"device"`
}

// serviceList represents the serviceList type in an UPnP xml description.
// Only the parts we care about are present and thus the xml may have more
// fields than present in the structure.
type serviceList struct {
	XMLName xml.Name  `xml:"serviceList"`
	Service []service `xml:"service"`
}

// device represents the device type in an UPnP xml description.
// Only the parts we care about are present and thus the xml may have more
// fields than present in the structure.
type device struct {
	XMLName     xml.Name    `xml:"device"`
	DeviceType  string      `xml:"deviceType"`
	DeviceList  deviceList  `xml:"deviceList"`
	ServiceList serviceList `xml:"serviceList"`
}

// specVersion represents the specVersion in a UPnP xml description.
// Only the parts we care about are present and thus the xml may have more
// fields than present in the structure.
type specVersion struct {
	XMLName xml.Name `xml:"specVersion"`
	Major   int      `xml:"major"`
	Minor   int      `xml:"minor"`
}

// root represents the Root document for a UPnP xml description.
// Only the parts we care about are present and thus the xml may have more
// fields than present in the structure.
type root struct {
	XMLName     xml.Name `xml:"root"`
	SpecVersion specVersion
	Device      device
}

// getChildDevice searches the children of device for a device with the given
// type.
func getChildDevice(d *device, deviceType string) *device {
	for i := range d.DeviceList.Device {
		if strings.Contains(d.DeviceList.Device[i].DeviceType, deviceType) {
			return &d.DeviceList.Device[i]
		}
	}
	return nil
}

// getChildDevice searches the service list of device for a service with the
// given type.
func getChildService(d *device, serviceType string) *service {
	for i := range d.ServiceList.Service {
		if strings.Contains(d.ServiceList.Service[i].ServiceType, serviceType) {
			return &d.ServiceList.Service[i]
		}
	}
	return nil
}

// getServiceURL parses the xml description at the given root url to find the
// url for the WANIPConnection service to be used for port forwarding.
func getServiceURL(localIP net.IP, rootURL string) (url, urnDomain string, err error) {

	webclient := &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				LocalAddr: &net.TCPAddr{
					IP: localIP,
				},
				Timeout:   3 * time.Second,
				DualStack: true,
			}).DialContext,
			TLSHandshakeTimeout:   3 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: 3 * time.Second,
	}

	r, err := webclient.Get(rootURL)
	if err != nil {
		return
	}
	defer r.Body.Close()

	if r.StatusCode >= 400 {
		err = errors.New("Unexpected status code " + strconv.Itoa(r.StatusCode))
		return
	}
	var root root
	err = xml.NewDecoder(r.Body).Decode(&root)
	if err != nil {
		return
	}
	a := &root.Device
	if !strings.Contains(a.DeviceType, "InternetGatewayDevice:1") {
		err = errors.New("no InternetGatewayDevice")
		return
	}
	b := getChildDevice(a, "WANDevice:1")
	if b == nil {
		err = errors.New("no WANDevice")
		return
	}
	c := getChildDevice(b, "WANConnectionDevice:1")
	if c == nil {
		err = errors.New("no WANConnectionDevice")
		return
	}
	d := getChildService(c, "WANIPConnection:1")
	if d == nil {
		// Some routers don't follow the UPnP spec, and put WanIPConnection under WanDevice,
		// instead of under WanConnectionDevice
		d = getChildService(b, "WANIPConnection:1")

		if d == nil {
			err = errors.New("no WANIPConnection")
			return
		}
	}
	// Extract the domain name, which isn't always 'schemas-upnp-org'
	urnDomain = strings.Split(d.ServiceType, ":")[1]
	url = combineURL(rootURL, d.ControlURL)
	return url, urnDomain, err
}

// combineURL appends subURL onto rootURL.
func combineURL(rootURL, subURL string) string {
	protocolEnd := "://"
	protoEndIndex := strings.Index(rootURL, protocolEnd)
	a := rootURL[protoEndIndex+len(protocolEnd):]
	rootIndex := strings.Index(a, "/")
	return rootURL[0:protoEndIndex+len(protocolEnd)+rootIndex] + subURL
}

// soapBody represents the <s:Body> element in a SOAP reply.
// fields we don't care about are elided.
type soapBody struct {
	XMLName xml.Name `xml:"Body"`
	Data    []byte   `xml:",innerxml"`
}

// soapEnvelope represents the <s:Envelope> element in a SOAP reply.
// fields we don't care about are elided.
type soapEnvelope struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    soapBody `xml:"Body"`
}

// soapRequests performs a soap request with the given parameters and returns
// the xml replied stripped of the soap headers. in the case that the request is
// unsuccessful the an error is returned.
func (n *upnpNAT) soapRequest(url, function, message, domain string) (replyXML []byte, err error) {
	fullMessage := "<?xml version=\"1.0\" ?>" +
		"<s:Envelope xmlns:s=\"http://schemas.xmlsoap.org/soap/envelope/\" s:encodingStyle=\"http://schemas.xmlsoap.org/soap/encoding/\">\r\n" +
		"<s:Body>" + message + "</s:Body></s:Envelope>"

	req, err := http.NewRequest("POST", url, strings.NewReader(fullMessage))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml ; charset=\"utf-8\"")
	req.Header.Set("User-Agent", "Darwin/10.0.0, UPnP/1.0, MiniUPnPc/1.3")
	req.Header.Set("SOAPAction", "\"urn:"+domain+":service:WANIPConnection:1#"+function+"\"")
	req.Header.Set("Connection", "Close")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")

	webclient := &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: (&net.Dialer{
				LocalAddr: &net.TCPAddr{
					IP: n.localIP,
				},
				Timeout:   3 * time.Second,
				DualStack: true,
			}).DialContext,
			TLSHandshakeTimeout:   3 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: 3 * time.Second,
	}

	r, err := webclient.Do(req)
	if err != nil {
		return nil, err
	}
	if r.Body != nil {
		defer r.Body.Close()
	}

	if r.StatusCode >= 400 {
		err = errors.New("Error " + strconv.Itoa(r.StatusCode) + " for " + function)
		r = nil
		return
	}
	var reply soapEnvelope
	err = xml.NewDecoder(r.Body).Decode(&reply)
	if err != nil {
		return nil, err
	}
	return reply.Body.Data, nil
}

// getExternalIPAddressResponse represents the XML response to a
// GetExternalIPAddress SOAP request.
type getExternalIPAddressResponse struct {
	XMLName           xml.Name `xml:"GetExternalIPAddressResponse"`
	ExternalIPAddress string   `xml:"NewExternalIPAddress"`
}

// GetExternalAddress implements the NAT interface by fetching the external IP
// from the UPnP router.
func (n *upnpNAT) GetExternalAddress() (addr net.IP, err error) {
	message := "<u:GetExternalIPAddress xmlns:u=\"urn:" + n.urnDomain + ":service:WANIPConnection:1\">\r\n</u:GetExternalIPAddress>"
	response, err := n.soapRequest(n.serviceURL, "GetExternalIPAddress", message, n.urnDomain)
	if err != nil {
		return nil, err
	}

	var reply getExternalIPAddressResponse
	err = xml.Unmarshal(response, &reply)
	if err != nil {
		return nil, err
	}

	addr = net.ParseIP(reply.ExternalIPAddress)
	if addr == nil {
		return nil, errors.New("unable to parse ip address")
	}
	return addr, nil
}

// AddPortMapping forwards a port at the UPnP router to the specified IP address and port. Lease duration is in seconds.
// FritzBox routers: Forwarding an already forwarded port results in no error. If the internal port is already forwarded under a different external port, error code 718 is returned in XML.
func (n *upnpNAT) AddPortMapping(protocol string, internalIP net.IP, internalPort, externalPort uint16, description string, leaseDuration int) (mappedExternalPort uint16, err error) {
	// A single concatenation would break ARM compilation.
	message := "<u:AddPortMapping xmlns:u=\"urn:" + n.urnDomain + ":service:WANIPConnection:1\">\r\n" +
		"<NewRemoteHost></NewRemoteHost><NewExternalPort>" + strconv.Itoa(int(externalPort))
	message += "</NewExternalPort><NewProtocol>" + strings.ToUpper(protocol) + "</NewProtocol>"
	message += "<NewInternalPort>" + strconv.Itoa(int(internalPort)) + "</NewInternalPort>" +
		"<NewInternalClient>" + internalIP.String() + "</NewInternalClient>" +
		"<NewEnabled>1</NewEnabled><NewPortMappingDescription>"
	message += description +
		"</NewPortMappingDescription><NewLeaseDuration>" + strconv.Itoa(leaseDuration) +
		"</NewLeaseDuration></u:AddPortMapping>"

	response, err := n.soapRequest(n.serviceURL, "AddPortMapping", message, n.urnDomain)
	if err != nil {
		// If UPnP is not allowed for the host (with FritzBox routers the user must manually enable it), the router returns "<errorCode>606</errorCode>"
		// in the XML response with HTTP code 500 Internal Server Error.
		// If the internal port is already forwarded under a different external port, error code 718 is returned in XML.
		return
	}

	// TODO: check response to see if the port was forwarded
	// If the port was not wildcard we don't get an reply with the port in it. Not sure about wildcard yet. miniupnpc just checks for error codes here.
	mappedExternalPort = externalPort
	_ = response

	return mappedExternalPort, err
}

// DeletePortMapping deletes a port mapping.
func (n *upnpNAT) DeletePortMapping(protocol string, externalPort uint16) (err error) {

	message := "<u:DeletePortMapping xmlns:u=\"urn:" + n.urnDomain + ":service:WANIPConnection:1\">\r\n" +
		"<NewRemoteHost></NewRemoteHost><NewExternalPort>" + strconv.Itoa(int(externalPort)) +
		"</NewExternalPort><NewProtocol>" + strings.ToUpper(protocol) + "</NewProtocol>" +
		"</u:DeletePortMapping>"

	response, err := n.soapRequest(n.serviceURL, "DeletePortMapping", message, n.urnDomain)
	if err != nil {
		return
	}

	// TODO: check response to see if the port was deleted
	// log.Println(message, response)
	_ = response
	return
}
