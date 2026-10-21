package meeting

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var meetPath = regexp.MustCompile(`^/([a-z]{3}-[a-z]{4}-[a-z]{3})/?$`)
var zoomHost = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+zoom\.us$`)
var zoomPath = regexp.MustCompile(`^/wc/([0-9]{9,11})/(?:join|start)/?$`)
var zoomAlternatePath = regexp.MustCompile(`^/wc/(?:join|start)/([0-9]{9,11})/?$`)
var jitsiPath = regexp.MustCompile(`^/([A-Za-z0-9_-]{1,128})/?$`)
var jitsiReserved = regexp.MustCompile(`(?i)^(?:static|libs|css|images|sounds|lang|fonts|external_api|http-bind|xmpp-websocket|about|privacy|terms|login|logout|register|settings|help)$`)
var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func classifyURL(input string) (Identity, error) {
	invalid := errors.New("Link meeting tidak didukung.")
	if len(input) > 4096 || strings.ContainsAny(input, "\\ \t\r\n") {
		return Identity{}, invalid
	}
	u, err := url.Parse(input)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || u.Opaque != "" {
		return Identity{}, invalid
	}
	host, path := strings.ToLower(u.Hostname()), u.EscapedPath()
	var provider, room, canonicalPath string
	switch {
	case host == "meet.google.com":
		if m := meetPath.FindStringSubmatch(path); m != nil {
			room = m[1]
		}
		provider, canonicalPath = "google_meet", "/"+room
	case host == "zoom.us" || zoomHost.MatchString(host):
		m := zoomPath.FindStringSubmatch(path)
		if m == nil {
			m = zoomAlternatePath.FindStringSubmatch(path)
		}
		if m != nil {
			room = m[1]
		}
		provider, host, canonicalPath = "zoom", "zoom.us", "/wc/"+room+"/join"
	case host == "meet.jit.si":
		if m := jitsiPath.FindStringSubmatch(path); m != nil && !jitsiReserved.MatchString(m[1]) {
			room = strings.ToLower(m[1])
		}
		provider, canonicalPath = "jitsi", "/"+room
	}
	if room == "" {
		return Identity{}, invalid
	}
	return Identity{Provider: provider, ProviderHost: host, ProviderRoomID: room, MeetingKey: provider + ":" + host + ":" + room, CanonicalURL: "https://" + host + canonicalPath}, nil
}
