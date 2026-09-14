# fanctl

Go CLI and root D-Bus daemon for ASRock X570 Creator pump/radiator fans via Linux hwmon (`nct6683`), plus an optional top-bar GTK GUI.

Requires **Go 1.27**. The tray GUI also needs **Rust**, **GTK4**, and **libadwaita**.

## Prerequisites

Load the NCT6683D sensor driver on AMD boards:

```bash
# one-time setup (already done on this machine if setup-nct6683.sh was run)
echo 'options nct6683 force=1' | sudo tee /etc/modprobe.d/nct6683.conf
echo nct6683 | sudo tee /etc/modules-load.d/nct6683.conf
sudo modprobe nct6683 force=1
```

Verify sensors:

```bash
sensors | sed -n '/nct6683/,/^$/p'
```

## Build

```bash
make build          # fanctl + fanctld
make build-gui      # Rust tray agent (needs gtk4 + libadwaita)
```

Install CLI, host-wide headers, and root daemon (no password prompts for GUI/D-Bus callers):

```bash
sudo make install
# config only: sudo make install-config
```

Install the top-bar GUI (autostart):

```bash
sudo make install-gui   # finds ~/.cargo/bin even under sudo
fanctl-gui &            # or log out/in
# all-in-one: sudo make deploy-gui
```

If `sudo` still cannot see Rust, build as your user first:

```bash
make build-gui && sudo make install-gui
```

## Usage (CLI)

```bash
fanctl status
sudo fanctl init-config
sudo fanctl max
sudo fanctl set -pwm 4 -pct 100
sudo fanctl set -name CPU_FAN1 -pct 70
sudo fanctl auto
```

### Commands

| Command | Description |
|---------|-------------|
| `status` | Show chip path, temperatures, fan RPM, PWM value and percent |
| `max` | Set all writable PWM outputs to 100% (manual mode) |
| `set -pwm N -pct P` | Set one header to `P` percent (0–100) |
| `set -name NAME -pct P` | Same, resolving `NAME` via `headers.yaml` |
| `auto` | Return writable PWM outputs to firmware automatic control (`pwmN_enable=0`) |
| `init-config` | Write example `/etc/fanctl/headers.yaml` (X570 Creator starting map; requires root) |

Use `-chip NAME` to target a different hwmon chip (default: `nct6683`).

CLI write commands still require root (`sudo`). The GUI talks to `fanctld` (already root) over D-Bus with **no password**.

On ASRock boards with the community `nct6683` driver, `pwmN_enable` uses `1` for manual and `0` for firmware automatic control (not the standard hwmon value `2` for auto).

## Top-bar GUI

After `sudo make install` + `sudo make install-gui`, a fan icon appears in the GNOME top bar (StatusNotifier / AppIndicator area, same place as Cursor/Happ).

1. Click the icon → popover with one slider per fan (name, RPM, 0–100%).
2. Drag a slider → `fanctld` writes PWM (debounced); no sudo/Polkit prompt.
3. Use **Auto** / **Max** in the popover footer; right-click the icon for Quit.

```mermaid
flowchart LR
  Icon["Top-bar icon"] -->|click| Popover["Adwaita popover"]
  Popover -->|D-Bus| Daemon["fanctld root"]
  Daemon --> Hwmon["hwmon PWM"]
```

| Piece | Role |
|-------|------|
| `fanctl-gui` | Tray agent + popover (Rust, GTK4/libadwaita) |
| `fanctld` | System service owning `org.fanctl.Control` |
| `/etc/fanctl/headers.yaml` | Silk-screen names for sliders |

Debug without waiting for autostart: `FANCTL_GUI_SHOW=1 fanctl-gui`.

## Named headers

Linux `nct6683` does not expose silk-screen labels (`CPU_FAN1`, `CHA_FAN2`, …). Indexes `fan1`…`fan6` are Super I/O channels. Map them yourself in a host-wide config file:

1. Install the example (or run `sudo fanctl init-config`):

```bash
sudo make install-config
# or: sudo fanctl init-config
```

2. Match channels to headers: with the PC powered, note `fanctl status` RPM, unplug **one** motherboard fan connector, run `status` again, and see which `fanN` drops to 0. Edit that index’s `name` / `note` in `/etc/fanctl/headers.yaml`.

3. Config search order: `-config PATH`, then `$FANCTL_CONFIG`, then `/etc/fanctl/headers.yaml`. Missing file is fine; status still prints `fanN:`. Named `set` and the GUI need a loaded config.

Example map (starting guess — verify before trusting):

```yaml
chip: nct6683
headers:
  1: { name: CPU_FAN1, note: "AIO radiator FAN plug" }
  2: { name: CPU_FAN2/WP, note: "AIO PUMP 4-pin" }
  3: { name: CHA_FAN1/WP, note: "AIO VRM plug" }
  4: { name: CHA_FAN2/WP, note: "" }
  5: { name: CHA_FAN3/WP, note: "" }
  6: { name: SB_FAN1, note: "chipset" }
```

With a config loaded, status looks like:

```
  fan1  CPU_FAN1  (AIO radiator FAN plug)
        1123 RPM  pwm=142 (56%)  control=auto  writable
```

`-name` matching is case-insensitive and ignores a trailing `/WP`.

## Writable PWM (ASRock DKMS driver)

The stock kernel `nct6683` driver exposes fan speeds but PWM files are often **read-only** on X570 Creator. To control fans from Linux, install the community driver:

```bash
sudo apt install dkms build-essential linux-headers-$(uname -r) git
git clone https://github.com/branchmispredictor/asrock-nct6683.git /tmp/asrock-nct6683
cd /tmp/asrock-nct6683 && sudo make dkms
```

Or run the helper on this machine:

```bash
sudo /home/legion/install-asrock-nct6683.sh
```

If PWM is read-only, `fanctl` prints an error pointing here.

## UEFI fallback

UEFI → **H/W Monitor** → **Fan Control** → set **CPU Fan** and **Water Pump** to **Full Speed**.
