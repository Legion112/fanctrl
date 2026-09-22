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

Install CLI, host-wide headers, and root daemon (no password prompts for GUI/D-Bus callers).
This also creates `/var/lib/fanctl` for the saved fan profile:

```bash
sudo make install
# config only: sudo make install-config
```

`install-config` keeps an existing `/etc/fanctl/headers.yaml` — your header names are the
result of unplugging connectors one at a time, so `make install` will not overwrite them. Use
`sudo make install-config-force` to replace it with the stock example.

Install the top-bar GUI (systemd user service, starts on login):

```bash
sudo make install-gui   # installs binary + enables fanctl-gui.service for your user
# all-in-one: sudo make deploy-gui
```

If `sudo` still cannot see Rust, build as your user first:

```bash
make build-gui && sudo make install-gui
```

Service status and logs:

```bash
systemctl --user status fanctl-gui
journalctl --user -u fanctl-gui -f
```

## Usage (CLI)

```bash
fanctl status
sudo fanctl init-config
sudo fanctl max
sudo fanctl set -pwm 4 -pct 100
sudo fanctl set -name CPU_FAN1 -pct 70
sudo fanctl auto
sudo fanctl save
sudo fanctl restore
fanctl saved
```

### Commands

| Command | Description |
|---------|-------------|
| `status` | Show chip path, temperatures, fan RPM, PWM value and percent |
| `max` | Set all writable PWM outputs to 100% (manual mode) |
| `set -pwm N -pct P` | Set one header to `P` percent (0–100) |
| `set -name NAME -pct P` | Same, resolving `NAME` via `headers.yaml` |
| `auto` | Return writable PWM outputs to firmware automatic control (`pwmN_enable=0`) |
| `save` | Save the current speeds as the profile restored at every boot |
| `restore` | Re-apply the saved profile now |
| `saved` | Print the saved profile (no root needed) |
| `init-config` | Write example `/etc/fanctl/headers.yaml` (X570 Creator starting map; requires root) |

Use `-chip NAME` to target a different hwmon chip (default: `nct6683`), and `-state PATH` to
point at a different saved profile (else `$FANCTL_STATE`, then `/var/lib/fanctl/state.yaml`).

CLI write commands still require root (`sudo`). The GUI talks to `fanctld` (already root) over D-Bus with **no password**.

On ASRock boards with the community `nct6683` driver, `pwmN_enable` uses `1` for manual and `0` for firmware automatic control (not the standard hwmon value `2` for auto).

## Top-bar GUI

After `sudo make install` + `sudo make install-gui`, `fanctl-gui.service` runs in your session and a fan icon appears in the GNOME top bar (StatusNotifier / AppIndicator area, same place as Cursor/Happ).

1. **Left-click** the icon → slider popover opens directly (Ubuntu shows the SNI menu instead if it is non-empty, so the tray menu is empty).
2. Drag a slider → `fanctld` writes PWM (debounced); no sudo/Polkit prompt.
3. Click **Save** to make the current speeds the boot default.
4. Use **Auto** / **Max** / **Quit** in the popover footer; click away or Escape to dismiss.

```mermaid
flowchart LR
  Icon["Top-bar icon"] -->|click| Popover["Adwaita popover"]
  Popover -->|D-Bus| Daemon["fanctld root"]
  Daemon --> Hwmon["hwmon PWM"]
```

| Piece | Role |
|-------|------|
| `fanctl-gui` | Tray agent + popover (Rust, GTK4/libadwaita); systemd **user** unit |
| `fanctld` | System service owning `org.fanctl.Control` |
| `/etc/fanctl/headers.yaml` | Silk-screen names for sliders |
| `/var/lib/fanctl/state.yaml` | Saved fan speeds, re-applied at boot |

```bash
systemctl --user status fanctl-gui
journalctl --user -u fanctl-gui -f
journalctl -u fanctld -f
# verbose GUI (slider / D-Bus timings):
systemctl --user edit fanctl-gui   # add: Environment=RUST_LOG=fanctl_gui=debug
# or one-shot:
RUST_LOG=fanctl_gui=debug FANCTL_GUI_SHOW=1 /usr/local/bin/fanctl-gui
```

## Named headers

Linux `nct6683` does not expose silk-screen labels (`CPU_FAN1`, `CHA_FAN2`, …). Indexes `fan1`…`fan6` are Super I/O channels. Map them yourself in a host-wide config file:

1. Install the example (or run `sudo fanctl init-config`):

```bash
sudo make install-config
# or: sudo fanctl init-config
```

Both leave an existing file alone; `sudo make install-config-force` or
`sudo fanctl init-config -force` replaces it.

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

## Saved profile

Calibrate once, keep it across reboots. Set your speeds however you like — GUI sliders,
`fanctl set`, `fanctl max` — then save them:

```bash
sudo fanctl save        # or click Save in the tray popover
```

`fanctld` re-applies the profile at every boot, so there is nothing to redo after a restart.
Nothing is written until you save, so you can experiment freely.

```bash
fanctl saved            # what will be restored
sudo fanctl restore     # re-apply it right now, without rebooting
```

The file is `/var/lib/fanctl/state.yaml`:

```yaml
version: 1
chip: nct6683
saved_at: 2026-09-22T14:03:11Z
fans:
  1: {mode: manual, pwm: 128, percent: 50}
  2: {mode: auto}
  4: {mode: manual, pwm: 255, percent: 100}
```

`pwm` (0–255) is what restore uses; `percent` is there for you to read. Percent is not a
lossless round trip — `pwm=100` is 39%, and 39% is `pwm=99` — so storing percent would drift
the speed by a step at every boot. A header saved as `mode: auto` is handed back to the
firmware on restore (`pwmN_enable=0`) rather than being driven to a duty cycle.

Read-only headers are left out of the profile; headers that have disappeared since the save
are skipped rather than failing the restore, so swapping a fan does not break the rest.

### How boot restore behaves

`fanctld` restores the profile before it announces itself on D-Bus, then re-checks it for the
first 15 seconds in case the board's EC reverts a write while it is still initialising. That
window closes the moment you change anything, so it never fights a slider. It also waits up to
60 seconds for the `nct6683` chip to appear, rather than exiting — exiting would burn systemd's
start limit and leave the daemon permanently failed.

```bash
journalctl -b -u fanctld | grep -i restor
```

### Caution

A saved profile is applied unattended at every boot, before anyone logs in. Saving a fan at a
very low duty cycle makes that permanent — `fanctl save` warns about any spinning fan below
20%, and `fanctld` logs a warning when it restores one, but neither refuses. Check your
temperatures after the first reboot with a new profile.

Note also that `dbus/org.fanctl.Control.conf` lets any local user call the daemon. That was
already true for setting a speed; `SaveState` means a local user can now make such a change
survive reboots. Tighten the policy if that matters on your machine.

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
