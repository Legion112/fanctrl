use std::cell::{Cell, RefCell};
use std::collections::HashMap;
use std::rc::Rc;
use std::sync::{mpsc, OnceLock};
use std::time::{Duration, Instant};

use gtk4::prelude::*;
use gtk4::{
    Align, Application, ApplicationWindow, Box as GtkBox, Button, CssProvider, Label, Orientation,
    Scale, STYLE_PROVIDER_PRIORITY_APPLICATION,
};
use ksni::blocking::TrayMethods;
use ksni::{Category, Tray};
use log::{debug, error, info, warn};
use zbus::blocking::Connection;
use zbus::proxy;

type FanTuple = (i32, String, String, i32, i32, i32, String, bool);

static DBUS_PROXY: OnceLock<ControlProxyBlocking<'static>> = OnceLock::new();

const UI_DEBOUNCE: Duration = Duration::from_millis(100);
/// After a local set, ignore hardware percent until it matches or this timeout elapses.
const LAST_SENT_GRACE: Duration = Duration::from_secs(2);

const POPOVER_CSS: &str = r#"
window.fanctl-popover {
  border-radius: 14px;
  background-color: alpha(@window_bg_color, 0.96);
}
window.fanctl-popover > box {
  margin: 4px;
}
label.fan-rpm {
  min-width: 18ch;
  font-feature-settings: "tnum";
}
"#;

/// Width for "9999 RPM · manual" so 3↔4 digit RPM does not resize the popover.
const RPM_LABEL_CHARS: i32 = 18;

fn format_rpm_status(rpm: i32, mode: &str) -> String {
    format!("{rpm:>4} RPM · {mode}")
}

#[proxy(
    interface = "org.fanctl.Control",
    default_service = "org.fanctl.Control",
    default_path = "/org/fanctl/Control",
    gen_blocking = true,
    blocking_name = "ControlProxyBlocking"
)]
trait Control {
    fn get_fans(&self) -> zbus::Result<Vec<FanTuple>>;
    fn set_percent(&self, index: u32, percent: u8) -> zbus::Result<()>;
    fn set_max(&self) -> zbus::Result<()>;
    fn set_auto(&self) -> zbus::Result<()>;
}

#[derive(Debug)]
enum TrayMsg {
    Toggle { x: i32, y: i32 },
}

struct FanTray {
    tx: mpsc::Sender<TrayMsg>,
}

impl Tray for FanTray {
    const MENU_ON_ACTIVATE: bool = false;

    fn id(&self) -> String {
        "org.fanctl.Gui".into()
    }

    fn category(&self) -> Category {
        Category::Hardware
    }

    fn title(&self) -> String {
        "Fans".into()
    }

    fn icon_name(&self) -> String {
        "fanctl-symbolic".into()
    }

    fn tool_tip(&self) -> ksni::ToolTip {
        ksni::ToolTip {
            title: "Fan control".into(),
            description: "Click to adjust fan speeds".into(),
            ..Default::default()
        }
    }

    fn activate(&mut self, x: i32, y: i32) {
        debug!("tray activate x={x} y={y}");
        let _ = self.tx.send(TrayMsg::Toggle { x, y });
    }

    fn menu(&self) -> Vec<ksni::MenuItem<Self>> {
        Vec::new()
    }
}

struct FanRow {
    name_label: Label,
    rpm_label: Label,
    scale: Scale,
    dragging: Rc<Cell<bool>>,
    pending_percent: Rc<Cell<Option<u8>>>,
    debounce_source: Rc<Cell<Option<glib::SourceId>>>,
    last_sent: Rc<Cell<Option<u8>>>,
    last_sent_at: Rc<Cell<Option<Instant>>>,
}

struct PopoverState {
    window: ApplicationWindow,
    list: GtkBox,
    status: Label,
    rows: RefCell<HashMap<u32, FanRow>>,
    updating: Rc<Cell<bool>>,
    suppress_focus_out: Rc<Cell<bool>>,
}

fn dbus_proxy() -> Result<&'static ControlProxyBlocking<'static>, String> {
    if let Some(proxy) = DBUS_PROXY.get() {
        return Ok(proxy);
    }
    info!("opening system D-Bus connection to org.fanctl.Control");
    let conn = Connection::system().map_err(|e| format!("system bus: {e}"))?;
    let conn = Box::leak(Box::new(conn));
    let proxy = ControlProxyBlocking::new(conn).map_err(|e| format!("proxy: {e}"))?;
    Ok(DBUS_PROXY.get_or_init(|| proxy))
}

fn call_set_percent(index: u32, percent: u8) {
    let start = Instant::now();
    match dbus_proxy().and_then(|p| p.set_percent(index, percent).map_err(|e| e.to_string())) {
        Ok(()) => {
            info!(
                "set_percent fan={index} pct={percent} ok in {}ms",
                start.elapsed().as_millis()
            );
        }
        Err(e) => {
            error!(
                "set_percent fan={index} pct={percent} failed in {}ms: {e}",
                start.elapsed().as_millis()
            );
        }
    }
}

fn flush_pending(
    index: u32,
    pending: &Cell<Option<u8>>,
    last_sent: &Cell<Option<u8>>,
    last_sent_at: &Cell<Option<Instant>>,
) {
    let Some(percent) = pending.take() else {
        return;
    };
    if last_sent.get() == Some(percent) {
        debug!("set_percent fan={index} pct={percent} skipped (unchanged)");
        return;
    }
    call_set_percent(index, percent);
    last_sent.set(Some(percent));
    last_sent_at.set(Some(Instant::now()));
}

fn schedule_set_percent(
    index: u32,
    percent: u8,
    pending: &Rc<Cell<Option<u8>>>,
    debounce_source: &Rc<Cell<Option<glib::SourceId>>>,
    last_sent: &Rc<Cell<Option<u8>>>,
    last_sent_at: &Rc<Cell<Option<Instant>>>,
) {
    pending.set(Some(percent));
    if let Some(id) = debounce_source.take() {
        id.remove();
    }
    let pending = Rc::clone(pending);
    let debounce_clear = Rc::clone(debounce_source);
    let last_sent = Rc::clone(last_sent);
    let last_sent_at = Rc::clone(last_sent_at);
    let id = glib::timeout_add_local_once(UI_DEBOUNCE, move || {
        debounce_clear.set(None);
        flush_pending(index, &pending, &last_sent, &last_sent_at);
    });
    debounce_source.set(Some(id));
}

fn any_row_busy(state: &PopoverState) -> bool {
    state.rows.borrow().values().any(|r| {
        r.dragging.get() || r.pending_percent.get().is_some()
    })
}

fn install_popover_css() {
    let provider = CssProvider::new();
    provider.load_from_data(POPOVER_CSS);
    if let Some(display) = gtk4::gdk::Display::default() {
        gtk4::style_context_add_provider_for_display(
            &display,
            &provider,
            STYLE_PROVIDER_PRIORITY_APPLICATION,
        );
    }
}

fn build_popover(app: &Application) -> Rc<PopoverState> {
    let window = ApplicationWindow::builder()
        .application(app)
        .title("")
        .resizable(false)
        .decorated(false)
        .default_width(340)
        .build();
    window.add_css_class("fanctl-popover");
    window.set_hide_on_close(true);

    let root = GtkBox::new(Orientation::Vertical, 8);
    root.set_margin_top(12);
    root.set_margin_bottom(12);
    root.set_margin_start(12);
    root.set_margin_end(12);

    let status = Label::new(Some("Loading…"));
    status.add_css_class("dim-label");
    status.set_halign(Align::Start);
    root.append(&status);

    let list = GtkBox::new(Orientation::Vertical, 10);
    root.append(&list);

    let buttons = GtkBox::new(Orientation::Horizontal, 8);
    buttons.set_halign(Align::End);
    let auto_btn = Button::with_label("Auto");
    let max_btn = Button::with_label("Max");
    let quit_btn = Button::with_label("Quit");
    buttons.append(&auto_btn);
    buttons.append(&max_btn);
    buttons.append(&quit_btn);
    root.append(&buttons);

    window.set_child(Some(&root));

    let state = Rc::new(PopoverState {
        window: window.clone(),
        list,
        status,
        rows: RefCell::new(HashMap::new()),
        updating: Rc::new(Cell::new(false)),
        suppress_focus_out: Rc::new(Cell::new(false)),
    });

    {
        let state = Rc::clone(&state);
        auto_btn.connect_clicked(move |_| {
            info!("Auto clicked");
            let start = Instant::now();
            match dbus_proxy().and_then(|p| p.set_auto().map_err(|e| e.to_string())) {
                Ok(()) => {
                    info!("set_auto ok in {}ms", start.elapsed().as_millis());
                    refresh(&state);
                }
                Err(e) => {
                    error!("set_auto failed in {}ms: {e}", start.elapsed().as_millis());
                    state.status.set_text(&format!("Auto failed: {e}"));
                }
            }
        });
    }
    {
        let state = Rc::clone(&state);
        max_btn.connect_clicked(move |_| {
            info!("Max clicked");
            let start = Instant::now();
            match dbus_proxy().and_then(|p| p.set_max().map_err(|e| e.to_string())) {
                Ok(()) => {
                    info!("set_max ok in {}ms", start.elapsed().as_millis());
                    refresh(&state);
                }
                Err(e) => {
                    error!("set_max failed in {}ms: {e}", start.elapsed().as_millis());
                    state.status.set_text(&format!("Max failed: {e}"));
                }
            }
        });
    }
    {
        let app = app.clone();
        quit_btn.connect_clicked(move |_| {
            info!("Quit clicked");
            app.quit();
        });
    }

    {
        let state = Rc::clone(&state);
        let key = gtk4::EventControllerKey::new();
        key.connect_key_pressed(move |_, key, _, _| {
            if key == gtk4::gdk::Key::Escape {
                debug!("popover dismissed (Escape)");
                state.window.set_visible(false);
                glib::Propagation::Stop
            } else {
                glib::Propagation::Proceed
            }
        });
        window.add_controller(key);
    }

    {
        let state = Rc::clone(&state);
        window.connect_close_request(move |_| {
            state.window.set_visible(false);
            glib::Propagation::Stop
        });
    }

    {
        let state = Rc::clone(&state);
        window.connect_notify_local(Some("is-active"), move |win, _| {
            if win.is_visible() && !win.is_active() && !state.suppress_focus_out.get() {
                debug!("popover dismissed (focus-out)");
                win.set_visible(false);
            }
        });
    }

    state
}

fn ensure_rows(state: &PopoverState, fans: &[FanTuple]) {
    let mut rows = state.rows.borrow_mut();
    let mut seen = Vec::new();

    for fan in fans {
        let (index, name, note, _rpm, _percent, _enable, _mode, writable) = fan;
        let idx = *index as u32;
        seen.push(idx);
        if rows.contains_key(&idx) {
            continue;
        }

        let row = GtkBox::new(Orientation::Vertical, 2);
        let header = GtkBox::new(Orientation::Horizontal, 8);

        let title = if note.is_empty() {
            name.clone()
        } else {
            format!("{name}  ({note})")
        };
        let name_label = Label::new(Some(&title));
        name_label.set_halign(Align::Start);
        name_label.set_hexpand(true);
        name_label.add_css_class("heading");

        let rpm_label = Label::new(Some(&format_rpm_status(0, "-")));
        rpm_label.add_css_class("dim-label");
        rpm_label.add_css_class("fan-rpm");
        rpm_label.set_width_chars(RPM_LABEL_CHARS);
        rpm_label.set_halign(Align::End);
        rpm_label.set_xalign(1.0);
        rpm_label.set_hexpand(false);
        header.append(&name_label);
        header.append(&rpm_label);

        let scale = Scale::with_range(Orientation::Horizontal, 0.0, 100.0, 1.0);
        scale.set_draw_value(true);
        scale.set_value_pos(gtk4::PositionType::Right);
        scale.set_hexpand(true);
        scale.set_sensitive(*writable);

        let dragging = Rc::new(Cell::new(false));
        let pending_percent = Rc::new(Cell::new(None::<u8>));
        let debounce_source = Rc::new(Cell::new(None::<glib::SourceId>));
        let last_sent = Rc::new(Cell::new(None::<u8>));
        let last_sent_at = Rc::new(Cell::new(None::<Instant>));

        {
            let dragging_begin = Rc::clone(&dragging);
            let gesture = gtk4::GestureDrag::new();
            gesture.connect_drag_begin(move |_, _, _| {
                dragging_begin.set(true);
                debug!("drag begin fan={idx}");
            });
            let dragging_end = Rc::clone(&dragging);
            let pending = Rc::clone(&pending_percent);
            let debounce_source = Rc::clone(&debounce_source);
            let last_sent = Rc::clone(&last_sent);
            let last_sent_at = Rc::clone(&last_sent_at);
            gesture.connect_drag_end(move |_, _, _| {
                dragging_end.set(false);
                if let Some(id) = debounce_source.take() {
                    id.remove();
                }
                debug!("drag end fan={idx}; flushing pending");
                flush_pending(idx, &pending, &last_sent, &last_sent_at);
            });
            scale.add_controller(gesture);
        }

        {
            let updating = Rc::clone(&state.updating);
            let pending = Rc::clone(&pending_percent);
            let debounce_source = Rc::clone(&debounce_source);
            let last_sent = Rc::clone(&last_sent);
            let last_sent_at = Rc::clone(&last_sent_at);
            scale.connect_value_changed(move |scale| {
                if updating.get() {
                    return;
                }
                let percent = scale.value().round() as u8;
                schedule_set_percent(
                    idx,
                    percent,
                    &pending,
                    &debounce_source,
                    &last_sent,
                    &last_sent_at,
                );
            });
        }

        row.append(&header);
        row.append(&scale);
        state.list.append(&row);

        rows.insert(
            idx,
            FanRow {
                name_label,
                rpm_label,
                scale,
                dragging,
                pending_percent,
                debounce_source,
                last_sent,
                last_sent_at,
            },
        );
    }

    let stale: Vec<u32> = rows.keys().copied().filter(|k| !seen.contains(k)).collect();
    for idx in stale {
        if let Some(row) = rows.remove(&idx) {
            if let Some(id) = row.debounce_source.take() {
                id.remove();
            }
            if let Some(parent) = row.scale.parent() {
                state.list.remove(&parent);
            }
        }
    }
}

fn apply_fans(state: &PopoverState, fans: &[FanTuple]) {
    ensure_rows(state, fans);
    state.updating.set(true);
    let rows = state.rows.borrow();
    for fan in fans {
        let (index, name, note, rpm, percent, _enable, mode, writable) = fan;
        let idx = *index as u32;
        let Some(row) = rows.get(&idx) else {
            continue;
        };
        let title = if note.is_empty() {
            name.clone()
        } else {
            format!("{name}  ({note})")
        };
        row.name_label.set_text(&title);
        row.rpm_label.set_text(&format_rpm_status(*rpm, mode));
        row.scale.set_sensitive(*writable);

        let hw = *percent as u8;
        if row.dragging.get() || row.pending_percent.get().is_some() {
            // Keep local thumb while the user is interacting.
            continue;
        }
        match row.last_sent.get() {
            Some(sent) if sent == hw => {
                // Hardware caught up with optimistic value.
                row.last_sent.set(None);
                row.last_sent_at.set(None);
                row.scale.set_value(hw as f64);
            }
            Some(sent) => {
                let aged_out = row
                    .last_sent_at
                    .get()
                    .map(|t| t.elapsed() >= LAST_SENT_GRACE)
                    .unwrap_or(true);
                if aged_out {
                    warn!(
                        "fan={idx}: hw pct={hw} still != last_sent={sent} after {:?}; accepting hw",
                        LAST_SENT_GRACE
                    );
                    row.last_sent.set(None);
                    row.last_sent_at.set(None);
                    row.scale.set_value(hw as f64);
                } else {
                    debug!("fan={idx}: skip scale sync hw={hw} last_sent={sent}");
                }
            }
            None => {
                row.scale.set_value(hw as f64);
            }
        }
    }
    state.updating.set(false);
}

fn refresh(state: &PopoverState) {
    let start = Instant::now();
    match dbus_proxy().and_then(|p| p.get_fans().map_err(|e| e.to_string())) {
        Ok(fans) => {
            info!(
                "get_fans ok count={} in {}ms",
                fans.len(),
                start.elapsed().as_millis()
            );
            if fans.is_empty() {
                state.status.set_text("No fans found");
            } else {
                state.status.set_text("Drag a slider to set fan speed");
            }
            apply_fans(state, &fans);
        }
        Err(e) => {
            warn!(
                "get_fans failed in {}ms: {e}",
                start.elapsed().as_millis()
            );
            state
                .status
                .set_text(&format!("fanctld unavailable: {e}"));
        }
    }
}

fn show_popover(state: &PopoverState, x: i32, y: i32) {
    let _ = (x, y);
    info!("showing popover");
    state.suppress_focus_out.set(true);
    let suppress = Rc::clone(&state.suppress_focus_out);
    glib::timeout_add_local_once(Duration::from_millis(250), move || {
        suppress.set(false);
    });
    state.window.present();
    state.window.grab_focus();
}

fn main() {
    env_logger::Builder::from_env(env_logger::Env::default().default_filter_or("info")).init();
    info!("fanctl-gui starting");

    let _ = gtk4::init();
    libadwaita::init().expect("libadwaita init");
    install_popover_css();

    let app = Application::builder()
        .application_id("org.fanctl.Gui")
        .build();

    app.connect_activate(|app| {
        let hold = app.hold();

        let (tx, rx) = mpsc::channel::<TrayMsg>();
        let tray = FanTray { tx: tx.clone() };
        match tray.spawn() {
            Ok(_handle) => info!("tray StatusNotifierItem registered"),
            Err(e) => error!("tray failed: {e}"),
        }

        let popover = build_popover(app);
        refresh(&popover);
        std::mem::forget(hold);

        let popover_msgs = Rc::clone(&popover);
        glib::timeout_add_local(Duration::from_millis(100), move || {
            while let Ok(msg) = rx.try_recv() {
                match msg {
                    TrayMsg::Toggle { x, y } => {
                        if popover_msgs.window.is_visible() {
                            info!("toggle: hiding popover");
                            popover_msgs.window.set_visible(false);
                        } else {
                            info!("toggle: showing popover");
                            refresh(&popover_msgs);
                            show_popover(&popover_msgs, x, y);
                        }
                    }
                }
            }
            glib::ControlFlow::Continue
        });

        let popover_poll = Rc::clone(&popover);
        glib::timeout_add_local(Duration::from_secs(2), move || {
            if popover_poll.window.is_visible() {
                if any_row_busy(&popover_poll) {
                    debug!("periodic refresh skipped (drag/pending)");
                } else {
                    debug!("periodic refresh");
                    refresh(&popover_poll);
                }
            }
            glib::ControlFlow::Continue
        });

        if std::env::var_os("FANCTL_GUI_SHOW").is_some() {
            refresh(&popover);
            show_popover(&popover, 0, 0);
        }
    });

    app.connect_shutdown(|_| {
        info!("fanctl-gui shutting down");
    });

    app.run();
}
