# hue2mqtt

A lightweight, high-performance Philips Hue Bridge emulator written in Go. It dynamically discovers Zigbee devices via Zigbee2MQTT and translates Hue API commands to MQTT payloads.

> [!IMPORTANT]
> This application is **only tested with Sleep as Android** for smart wake-up alarm integrations. Other home automation platforms or official Philips Hue apps will probably fail to connect or control lights because they require more advanced Hue API features that are not implemented here.

If you need a complete, general-purpose Hue Bridge emulator, check out these much more advanced projects:
- [diyHue](https://github.com/diyhue/diyHue) — A feature-rich emulation server that this project draws inspiration from.
- [bifrost](https://github.com/chrivers/bifrost) — A highly capable Hue bridge emulator.

---

## Features

- **Philips Hue Bridge Emulation**: Emulates a Philips Hue Bridge (BSB002) supporting UPnP discovery (`description.xml`) and the Hue V1 REST API.
- **SSDP & mDNS Discovery**: Automatically advertises itself on the local network via SSDP (UDP multicast) and mDNS (`_hue._tcp`), allowing apps to automatically discover the bridge.
- **Dynamic Autodiscovery**: Subscribes to `zigbee2mqtt/bridge/devices` on startup, automatically mapping your Zigbee bulbs and plugs to Hue IDs.
- **Capability Auto-detection**: Automatically inspects Z2M device capabilities (`definition.exposes`) to dynamically map them to the correct Hue type:
  - `on_off` (Smart plugs/switches)
  - `dimmable` (Dimmable bulbs)
  - `color_temperature` (Tunable white bulbs)
  - `color` (RGB color bulbs)
  - `extended_color` (RGB + Tunable white bulbs)
- **Live Configuration Hot-Reload**: Watches configuration files for modifications (using `fsnotify`) to dynamically reload log levels, pairing controls, and light overrides without restarting.
- **Configurable Logging Levels**:
  - `info`: Clean startup message and error tracking only (bypasses noisy polling routes).
  - `simple`: One-line summaries for all incoming requests.
  - `verbose`: Full multi-line dumps of raw HTTP requests and responses for troubleshooting.

---

## Configuration

The application uses `config.yaml` as the main template, and supports an optional local override file `config.local.yaml` (which is gitignored) for storing your local broker credentials and broker IPs (similar to C#'s `appsettings.json` and user secrets).

### Main configuration (`config.yaml`)
Create this file in the same directory as the executable:

```yaml
bridge:
  name: "hue2mqtt"
  mac: "AA:BB:CC:DD:EE:FF"    # Fake MAC address used for Bridge ID derivation
  http_port: 80               # Port the bridge serves (Hue clients expect port 80)
  link_button: true           # Enable to accept new app pairings, disable afterwards
  log_level: "info"           # Options: "info", "simple", "verbose"

mqtt:
  server: "192.168.1.100"     # Your MQTT Broker IP
  port: 1883                  # Your MQTT Broker Port
  username: ""                # Optional username
  password: ""                # Optional password

# Optional overrides: match friendly_name to change display names or force specific capabilities
lights:
  - friendly_name: "example_light_1"
    name: "Bedroom Light 1"
    capabilities: "extended_color"
```

### Local overrides (`config.local.yaml`)
You can define overrides locally without modifying the tracked `config.yaml`:
```yaml
mqtt:
  server: "192.168.11.141"     # Your real MQTT Broker IP
```

---

## Usage

### Run locally
To run the server locally:
```bash
go run main.go
```
*Note: Because the bridge emulates a Hue Bridge, it defaults to port `80`. Running on port `80` may require root/administrative privileges depending on your OS.*

### Running Tests
To run the automated unit test suite:
```bash
go test -v ./...
```
