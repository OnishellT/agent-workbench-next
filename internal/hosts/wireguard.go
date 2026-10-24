package hosts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os/exec"
	"time"
)

type wireGuardDiagnostic struct {
	HostID               string  `json:"host_id"`
	HostName             string  `json:"host_name"`
	Address              string  `json:"address"`
	Interface            *string `json:"interface"`
	WireGuard            bool    `json:"wireguard"`
	MTU                  *int    `json:"mtu"`
	MainDefaultUsesWG    bool    `json:"main_default_uses_wireguard"`
	Scope                string  `json:"scope"`
	ConfigurationChanged bool    `json:"configuration_changed"`
}

type ipJSONRunner func(context.Context, ...string) ([]byte, error)

const maxIPJSON = 1_000_000

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	if b.Len()+len(value) > b.limit {
		return 0, errors.New("salida iproute2 demasiado grande")
	}
	return b.Buffer.Write(value)
}

func runIPJSON(ctx context.Context, args ...string) ([]byte, error) {
	ip, err := exec.LookPath("ip")
	if err != nil {
		return nil, errors.New("iproute2 no está disponible para consultar la ruta")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, ip, append([]string{"-j"}, args...)...)
	var output limitedBuffer
	output.limit = maxIPJSON
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("la consulta de ruta agotó el tiempo")
		}
		return nil, errors.New("no se pudo consultar la ruta del host")
	}
	return output.Bytes(), nil
}

func inspectWireGuardRoute(ctx context.Context, host Host, read ipJSONRunner) (wireGuardDiagnostic, error) {
	address := net.ParseIP(host.Address)
	if address == nil {
		return wireGuardDiagnostic{}, errors.New("usa la IP WireGuard del host registrado; no se resuelven nombres")
	}
	if address.IsUnspecified() || address.IsMulticast() || address.IsLoopback() {
		return wireGuardDiagnostic{}, errors.New("la dirección SSH no puede ser unspecified, multicast ni loopback")
	}
	family := "-4"
	if address.To4() == nil {
		family = "-6"
	}
	linksRaw, err := read(ctx, "link", "show", "type", "wireguard")
	if err != nil {
		return wireGuardDiagnostic{}, err
	}
	routesRaw, err := read(ctx, family, "route", "get", address.String())
	if err != nil {
		return wireGuardDiagnostic{}, err
	}
	defaultsRaw, err := read(ctx, family, "route", "show", "default")
	if err != nil {
		return wireGuardDiagnostic{}, err
	}
	var links []struct {
		Name string `json:"ifname"`
		MTU  *int   `json:"mtu"`
	}
	var routes []struct {
		Device *string `json:"dev"`
	}
	var defaults []struct {
		Device *string `json:"dev"`
	}
	for _, item := range []struct {
		raw    []byte
		result any
	}{{linksRaw, &links}, {routesRaw, &routes}, {defaultsRaw, &defaults}} {
		if err := json.Unmarshal(item.raw, item.result); err != nil {
			return wireGuardDiagnostic{}, errors.New("el sistema no devolvió una ruta JSON válida")
		}
	}
	wireGuardLinks := make(map[string]*int, len(links))
	for _, link := range links {
		if link.Name != "" {
			wireGuardLinks[link.Name] = link.MTU
		}
	}
	var interfaceName *string
	var mtu *int
	wireGuard := false
	if len(routes) > 0 && routes[0].Device != nil {
		interfaceName = routes[0].Device
		mtu, wireGuard = wireGuardLinks[*interfaceName]
	}
	mainDefault := false
	for _, route := range defaults {
		if route.Device != nil {
			if _, ok := wireGuardLinks[*route.Device]; ok {
				mainDefault = true
				break
			}
		}
	}
	return wireGuardDiagnostic{HostID: host.ID, HostName: host.Name, Address: address.String(), Interface: interfaceName, WireGuard: wireGuard, MTU: mtu, MainDefaultUsesWG: mainDefault, Scope: "route-only", ConfigurationChanged: false}, nil
}
