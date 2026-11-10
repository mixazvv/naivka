package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// Minimal bounded reader of the public ClientConfig protobuf wire format.
// Remote profile fields we cannot preserve are rejected, never guessed.
// Reference: enfein/mieru pkg/appctl/proto/{clientcfg,base}.proto.
type mieruField struct {
	name, child string
	repeated    bool
	values      []string
}

var mieruSchemas = map[string]map[int]mieruField{
	"config":    {1: {name: "profiles", child: "profile", repeated: true}},
	"profile":   {1: {name: "profileName"}, 2: {name: "user", child: "user"}, 3: {name: "servers", child: "endpoint", repeated: true}, 4: {name: "mtu"}, 5: {name: "multiplexing", child: "multiplex"}, 6: {name: "handshakeMode", values: []string{"HANDSHAKE_DEFAULT", "HANDSHAKE_STANDARD", "HANDSHAKE_NO_WAIT"}}},
	"user":      {1: {name: "name"}, 2: {name: "password"}, 3: {name: "hashedPassword"}},
	"endpoint":  {1: {name: "ipAddress"}, 2: {name: "domainName"}, 3: {name: "portBindings", child: "binding", repeated: true}},
	"binding":   {1: {name: "port"}, 2: {name: "protocol", values: []string{"UNKNOWN_TRANSPORT_PROTOCOL", "UDP", "TCP"}}, 3: {name: "portRange"}},
	"multiplex": {1: {name: "level", values: []string{"MULTIPLEXING_DEFAULT", "MULTIPLEXING_OFF", "MULTIPLEXING_LOW", "MULTIPLEXING_MIDDLE", "MULTIPLEXING_HIGH"}}},
}

func decodeMieruMessage(b []byte, schema string, depth int) (M, error) {
	if depth > 8 || len(b) > 1<<20 {
		return nil, errors.New("Mieru: слишком большой профиль")
	}
	out := M{}
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 || key>>3 == 0 {
			return nil, errors.New("Mieru: повреждён protobuf")
		}
		b = b[n:]
		f, known := mieruSchemas[schema][int(key>>3)]
		var value any
		switch key & 7 {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 || v > 1<<31-1 {
				return nil, errors.New("Mieru: неверное число")
			}
			b = b[n:]
			value = int(v)
			if len(f.values) > 0 {
				if v >= uint64(len(f.values)) {
					return nil, errors.New("Mieru: неизвестный режим")
				}
				value = f.values[v]
			}
		case 2:
			length, n := binary.Uvarint(b)
			if n <= 0 || length > uint64(len(b)-n) {
				return nil, errors.New("Mieru: повреждена длина поля")
			}
			b = b[n:]
			data := b[:int(length)]
			b = b[int(length):]
			value = string(data)
			if f.child != "" {
				var err error
				value, err = decodeMieruMessage(data, f.child, depth+1)
				if err != nil {
					return nil, err
				}
			}
		default:
			return nil, errors.New("Mieru: неподдерживаемое поле protobuf")
		}
		if !known {
			// Local listener options and server-side quotas cannot affect our client.
			if schema == "config" || schema == "user" {
				continue
			}
			return nil, fmt.Errorf("Mieru: расширенное поле %s/%d пока не поддерживается", schema, key>>3)
		}
		if f.repeated {
			list, _ := out[f.name].([]any)
			out[f.name] = append(list, value)
		} else {
			out[f.name] = value
		}
	}
	return out, nil
}

func parseMieruFull(link string) ([]Server, error) {
	if len(link) > 2<<20 {
		return nil, errors.New("Mieru: слишком длинная ссылка")
	}
	b, err := base64.StdEncoding.DecodeString(link[len("mieru://"):])
	if err != nil {
		return nil, errors.New("Mieru: повреждён Base64")
	}
	cfg, err := decodeMieruMessage(b, "config", 0)
	if err != nil {
		return nil, err
	}
	js, _ := json.Marshal(cfg)
	servers, skipped, _ := parseMieruJSON(string(js))
	if skipped != 0 || len(servers) == 0 {
		return nil, errors.New("Mieru: профиль содержит неподдерживаемые или неполные параметры")
	}
	return servers, nil
}
