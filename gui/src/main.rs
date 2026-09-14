use std::cell::{Cell, RefCell};
use std::collections::HashMap;
use std::rc::Rc;
use std::sync::mpsc;
use std::time::Duration;

use gtk4::prelude::*;
use gtk4::{
    Align, Application, ApplicationWindow, Box as GtkBox, Button, Label, Orientation, Scale,
};
use ksni::blocking::TrayMethods;
use ksni::{Category, MenuItem, Tray};
use zbus::blocking::Connection;
use zbus::proxy;

type FanTuple = (i32, String, String, i32, i32, i32, String, bool);

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
    Quit,
}

struct FanTray {
    tx: mpsc::Sender<TrayMsg>,
}

impl Tray for FanTray {
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
        // Installed by make install-gui; falls back if theme has weather-windy.
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
        let _ = self.tx.send(TrayMsg::Toggle { x, y });
    }

    fn menu(&self) -> Vec<MenuItem<Self>> {
        use ksni::menu::*;
        vec![
            StandardItem {
                label: "Open fan controls".into(),
                activate: Box::new(|this: &mut Self| {
                    let _ = this.tx.send(TrayMsg::Toggle { x: 0, y: 0 });
                }),
                ..Default::default()
            }
            .into(),
            MenuItem::Separator,
            StandardItem {
                label: "Quit".into(),
                icon_name: "application-exit".into(),
                activate: Box::new(|this: &mut Self| {
                    let _ = this.tx.send(TrayMsg::Quit);
                }),
                ..Default::default()
            }
            .into(),
        ]
    }
}

struct FanRow {
    name_label: Label,
    rpm_label: Label,
    scale: Scale,
    dragging: Rc<Cell<bool>>,
}

struct PopoverState {
    window: ApplicationWindow,
    list: GtkBox,
    status: Label,
    rows: RefCell<HashMap<u32, FanRow>>,
    updating: Rc<Cell<bool>>,
}

fn dbus_proxy() -> Result<ControlProxyBlocking<'static>, String> {
    let conn = Connection::system().map_err(|e| format!("system bus: {e}"))?;
    // Leak connection for 'static proxy lifetime used by UI callbacks.
    let conn = Box::leak(Box::new(conn));
    ControlProxyBlocking::new(conn).map_err(|e| format!("proxy: {e}"))
}

fn fetch_fans(proxy: &ControlProxyBlocking<'_>) -> Result<Vec<FanTuple>, String> {
    proxy.get_fans().map_err(|e| format!("{e}"))
}

fn build_popover(app: &Application) -> Rc<PopoverState> {
    let window = ApplicationWindow::builder()
        .application(app)
        .title("Fans")
        .resizable(false)
        .decorated(true)
        .default_width(320)
        .build();
    window.add_css_class("osd");

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
    buttons.append(&auto_btn);
    buttons.append(&max_btn);
    root.append(&buttons);

    window.set_child(Some(&root));

    let state = Rc::new(PopoverState {
        window: window.clone(),
        list,
        status,
        rows: RefCell::new(HashMap::new()),
        updating: Rc::new(Cell::new(false)),
    });

    {
        let state = Rc::clone(&state);
        auto_btn.connect_clicked(move |_| {
            match dbus_proxy().and_then(|p| p.set_auto().map_err(|e| e.to_string())) {
                Ok(()) => refresh(&state),
                Err(e) => state.status.set_text(&format!("Auto failed: {e}")),
            }
        });
    }
    {
        let state = Rc::clone(&state);
        max_btn.connect_clicked(move |_| {
            match dbus_proxy().and_then(|p| p.set_max().map_err(|e| e.to_string())) {
                Ok(()) => refresh(&state),
                Err(e) => state.status.set_text(&format!("Max failed: {e}")),
            }
        });
    }

    {
        let state = Rc::clone(&state);
        let key = gtk4::EventControllerKey::new();
        key.connect_key_pressed(move |_, key, _, _| {
            if key == gtk4::gdk::Key::Escape {
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

        let rpm_label = Label::new(Some("— RPM"));
        rpm_label.add_css_class("dim-label");

        header.append(&name_label);
        header.append(&rpm_label);

        let scale = Scale::with_range(Orientation::Horizontal, 0.0, 100.0, 1.0);
        scale.set_draw_value(true);
        scale.set_value_pos(gtk4::PositionType::Right);
        scale.set_hexpand(true);
        scale.set_sensitive(*writable);

        let dragging = Rc::new(Cell::new(false));
        {
            let dragging_begin = Rc::clone(&dragging);
            let gesture = gtk4::GestureDrag::new();
            gesture.connect_drag_begin(move |_, _, _| dragging_begin.set(true));
            let dragging_end = Rc::clone(&dragging);
            gesture.connect_drag_end(move |_, _, _| dragging_end.set(false));
            scale.add_controller(gesture);
        }

        {
            let updating = Rc::clone(&state.updating);
            scale.connect_value_changed(move |scale| {
                if updating.get() {
                    return;
                }
                let percent = scale.value().round() as u8;
                if let Ok(proxy) = dbus_proxy() {
                    let _ = proxy.set_percent(idx, percent);
                }
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
            },
        );
    }

    // Remove rows for fans that disappeared (rare).
    let stale: Vec<u32> = rows.keys().copied().filter(|k| !seen.contains(k)).collect();
    for idx in stale {
        if let Some(row) = rows.remove(&idx) {
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
        row.rpm_label.set_text(&format!("{rpm} RPM · {mode}"));
        row.scale.set_sensitive(*writable);
        if !row.dragging.get() {
            row.scale.set_value(*percent as f64);
        }
    }
    state.updating.set(false);
}

fn refresh(state: &PopoverState) {
    match dbus_proxy().and_then(|p| fetch_fans(&p)) {
        Ok(fans) => {
            if fans.is_empty() {
                state.status.set_text("No fans found");
            } else {
                state.status.set_text("Drag a slider to set fan speed");
            }
            apply_fans(state, &fans);
        }
        Err(e) => {
            state
                .status
                .set_text(&format!("fanctld unavailable: {e}"));
        }
    }
}

fn position_near(window: &ApplicationWindow, x: i32, y: i32) {
    // Best-effort: place near click; GNOME Wayland may ignore absolute moves.
    if x > 0 && y > 0 {
        let _ = (x, y);
        // gtk4 ApplicationWindow has no set_position; present() is enough under Wayland.
    }
    window.present();
}

fn main() {
    let _ = gtk4::init();
    libadwaita::init().expect("libadwaita init");

    let app = Application::builder()
        .application_id("org.fanctl.Gui")
        .build();

    app.connect_activate(|app| {
        let hold = app.hold();

        let (tx, rx) = mpsc::channel::<TrayMsg>();
        let tray = FanTray { tx: tx.clone() };
        match tray.spawn() {
            Ok(_handle) => {}
            Err(e) => {
                eprintln!("fanctl-gui: tray failed: {e}");
            }
        }

        let popover = build_popover(app);
        refresh(&popover);
        // Keep the application alive with no visible window (tray-only).
        std::mem::forget(hold);

        // Poll tray messages into the GTK main loop.
        let popover_msgs = Rc::clone(&popover);
        let app_quit = app.clone();
        glib::timeout_add_local(Duration::from_millis(100), move || {
            while let Ok(msg) = rx.try_recv() {
                match msg {
                    TrayMsg::Toggle { x, y } => {
                        if popover_msgs.window.is_visible() {
                            popover_msgs.window.set_visible(false);
                        } else {
                            refresh(&popover_msgs);
                            position_near(&popover_msgs.window, x, y);
                        }
                    }
                    TrayMsg::Quit => {
                        app_quit.quit();
                    }
                }
            }
            glib::ControlFlow::Continue
        });

        // Refresh RPM while the popover is open.
        let popover_poll = Rc::clone(&popover);
        glib::timeout_add_local(Duration::from_secs(2), move || {
            if popover_poll.window.is_visible() {
                refresh(&popover_poll);
            }
            glib::ControlFlow::Continue
        });

        // Allow launching without tray: show once.
        if std::env::var_os("FANCTL_GUI_SHOW").is_some() {
            refresh(&popover);
            popover.window.present();
        }
    });

    app.run();
}
