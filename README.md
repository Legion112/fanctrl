# fanctl

Go CLI for controlling ASRock X570 Creator pump and radiator fans via Linux hwmon (`nct6683`).

Requires **Go 1.27**.

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
go build -o fanctl ./cmd/fanctl
```

## Usage

```bash
fanctl status
sudo fanctl max
sudo fanctl set -pwm 4 -pct 100
sudo fanctl auto
```

### Commands

| Command | Description |
|---------|-------------|
| `status` | Show chip path, temperatures, fan RPM, PWM value and percent |
| `max` | Set all writable PWM outputs to 100% (manual mode) |
| `set -pwm N -pct P` | Set one header to `P` percent (0–100) |
| `auto` | Return writable PWM outputs to firmware automatic control (`pwmN_enable=0`) |

Use `-chip NAME` to target a different hwmon chip (default: `nct6683`).

Write commands require root (`sudo`).

On ASRock boards with the community `nct6683` driver, `pwmN_enable` uses `1` for manual and `0` for firmware automatic control (not the standard hwmon value `2` for auto).

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
