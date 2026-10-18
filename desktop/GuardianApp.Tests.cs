using System;
using System.Collections.Generic;
using System.Drawing;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Text;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Threading.Tasks;
using System.Windows.Forms;
using ResponsesGuardian.Desktop;

internal static class DesktopTests
{
    private static int exitCode = 1;
    private static string root;
    private static string captures;
    private static bool unhandledFailure;
    [DllImport("user32.dll",EntryPoint="GetWindowLongPtrW")] private static extern IntPtr GetWindowLongPtr(IntPtr window,int index);
    [DllImport("user32.dll")] private static extern IntPtr SendMessage(IntPtr window,int message,IntPtr wparam,IntPtr lparam);

    [STAThread]
    private static int Main(string[] args)
    {
        root = args[0]; captures = args[1];
        Application.EnableVisualStyles();
        Application.SetCompatibleTextRenderingDefault(false);
        var context = new ApplicationContext();
        Application.SetUnhandledExceptionMode(UnhandledExceptionMode.CatchException);
        Application.ThreadException += delegate(object sender,System.Threading.ThreadExceptionEventArgs e) {
            unhandledFailure=true; exitCode=1; Console.Error.WriteLine("UNHANDLED_UI_EXCEPTION: "+e.Exception);
            context.ExitThread();
        };
        var first = NewForm();
        first.Shown += async delegate {
            try { await Run(first); if(!unhandledFailure) { exitCode = 0; Console.WriteLine("DESKTOP_UI_TESTS_PASSED"); } }
            catch (Exception error) { Console.Error.WriteLine(error); }
            finally { foreach (Form form in new List<Form>(OpenForms())) form.Close(); context.ExitThread(); }
        };
        first.Show();
        Application.Run(context);
        return exitCode;
    }

    private static IEnumerable<Form> OpenForms() { foreach (Form form in Application.OpenForms) yield return form; }
    private static GuardianForm NewForm() { return new GuardianForm(root,false) { ShowInTaskbar = false, StartPosition = FormStartPosition.Manual, Location = new Point(-1800, -1800) }; }
    private static T Find<T>(Control form, string name) where T : Control { var found = form.Controls.Find(name, true); if (found.Length != 1) throw new Exception("Missing control " + name); return (T)found[0]; }
    private static void Require(bool condition, string message) { if (!condition) throw new Exception(message); }
    private static async Task Wait(GuardianForm form, bool startup = false)
    {
        var deadline = DateTime.UtcNow.AddSeconds(45);
        while (form.IsBusy || (startup && !form.IsConnected && form.LastError == ""))
        {
            if (DateTime.UtcNow > deadline) throw new Exception("Desktop operation timeout");
            await Task.Delay(40);
        }
    }
    private static async Task Click(GuardianForm form, string name)
    {
        var button = Find<Button>(form, name);
        Require(button.Enabled, "Control is disabled: " + name);
        button.PerformClick();
        await Wait(form);
    }
    private static void Capture(GuardianForm form, string name)
    {
        form.PerformLayout();
        using (var image = new Bitmap(form.Width, form.Height))
        {
            form.DrawToBitmap(image, new Rectangle(Point.Empty, image.Size));
            image.Save(Path.Combine(captures, name + ".png"), System.Drawing.Imaging.ImageFormat.Png);
        }
    }
    private static async Task Run(GuardianForm first)
    {
        CheckHistoryReconciliation();
        await Wait(first, true);
        Require(first.IsConnected && GuardianRuntime.Text(first.Current, "mode") == "off", "First start did not use off: " + first.LastError);
        Require(!Find<Button>(first,"ConnectProxy").Enabled&&Find<Button>(first,"ConnectProxy").Text=="已连接","Connected proxy still offers an active connect button");
        Require(Find<Button>(first,"RefreshState").Enabled,"Connected proxy cannot be refreshed");
        await CheckQuietOperations(first);
        CheckPublicSkin(first);
        CheckDpiPainting();
        await CheckPageHeightBudget();
        await CheckWindowLifecycle(first);
        await CheckFluidLayout(first);
        await CheckLiveResize(first);
        string pid = GuardianRuntime.Text(first.Current, "pid");
        Capture(first, "desktop-off");
        await Click(first, "Mode-always");
        Require(GuardianRuntime.Text(first.Current, "mode") == "always", "Mode button failed: " + first.LastError);
        Capture(first, "desktop-always");
        await Click(first, "NavSettings");
        await CheckEditing(first);
        Capture(first, "desktop-settings");
        Find<RetryInput>(first, "RequestRetries").Value = 3;
        Find<RetryInput>(first, "StreamRetries").Value = 0;
        await Click(first, "SaveSettings");
        Require(first.LastError == "", "Config save failed: " + first.LastError);
        var saved = GuardianRuntime.Map(first.Current, "saved");
        Require(GuardianRuntime.Number(saved, "request_max_retries") == 3 && GuardianRuntime.Number(saved, "stream_max_retries") == 0, "Retry values were not saved");
        var listenField=Find<TextBox>(first,"Listen"); string savedListen=listenField.Text;
        listenField.Text=savedListen=="127.0.0.1:23456"?"127.0.0.1:23457":"127.0.0.1:23456";
        await Click(first,"SaveSettings");
        var restartNote=Find<Label>(first,"PendingRestart");
        Require(first.LastError==""&&restartNote.Visible&&restartNote.Height>=50*UI.Dpi(first)/96F,"Restart guidance was collapsed after saving a new address");
        await Click(first,"NavOverview"); await Click(first,"NavSettings");
        Require(restartNote.Visible&&restartNote.Height>=50*UI.Dpi(first)/96F,"Restart guidance collapsed after changing pages");
        Capture(first,"desktop-pending-restart");
        listenField.Text=savedListen; await Click(first,"SaveSettings");
        Require(first.LastError==""&&!restartNote.Visible,"Restart guidance did not clear after restoring the active address");
        string settingsFile = Path.Combine(root, "runtime", "settings.json");
        string goodSettings = File.ReadAllText(settingsFile);
        await Click(first,"UndoSettings");
        Require(Find<Button>(first,"SaveSettings").Enabled&&File.ReadAllText(settingsFile)==goodSettings,"Undo wrote settings or did not compare with the new saved baseline");
        await Click(first,"RedoSettings");
        Require(!Find<Button>(first,"SaveSettings").Enabled,"Redo did not return to the saved baseline");
        Find<TextBox>(first, "Provider").Text = "missing-provider";
        await Click(first, "SaveSettings");
        Require(first.LastError != "" && File.ReadAllText(settingsFile) == goodSettings, "Invalid provider was saved");
        Capture(first, "desktop-error");
        Find<TextBox>(first, "Provider").Text = "custom";
        Require(first.LastError==""&&!Find<Button>(first,"SaveSettings").Enabled,"Returning to saved values retained a stale validation error or dirty flag");
        await Click(first, "NavOverview");
        var active = GuardianRuntime.Map(first.Current, "active");
        string listen = GuardianRuntime.Text(active, "listen");
        first.Close();

        var second = NewForm(); second.Show(); await Wait(second, true);
        Require(second.IsConnected && GuardianRuntime.Text(second.Current, "pid") == pid && GuardianRuntime.Text(second.Current, "mode") == "always", "Closing/reopening the window failed: " + second.LastError);
        Uri endpoint = new Uri("http://" + listen);
        using (var connection = new TcpClient())
        {
            connection.Connect(endpoint.DnsSafeHost, endpoint.Port);
            byte[] request = Encoding.ASCII.GetBytes("GET /__responses_guardian/health HTTP/1.1\r\nHost: " + endpoint.Authority + "\r\nConnection: keep-alive\r\n\r\n");
            connection.GetStream().Write(request, 0, request.Length);
            await Click(second, "StopProxy");
            Require(second.IsConnected && second.LastError.Contains("Close Codex") && GuardianRuntime.Text(second.Current, "pid") == pid, "Stop button interrupted an active connection or hid the refusal");
        }
        second.Close();

        File.WriteAllText(settingsFile, "invalid fixture JSON", new UTF8Encoding(false));
        string modeFile = Path.Combine(root, "runtime", "mode.json");
        File.WriteAllText(modeFile, "invalid fixture mode", new UTF8Encoding(false));
        try
        {
            var failed = NewForm(); failed.Show(); await Wait(failed, true);
            Require(!failed.IsConnected && failed.LastError != "", "Startup failure did not remain in the application window");
            Require(Find<Button>(failed,"ConnectProxy").Enabled&&Find<Button>(failed,"ConnectProxy").Text=="连接代理","Disconnected proxy cannot be connected again");
            await CheckStableButtons(failed,()=>Click(failed,"Mode-off"),"offline-mode",new[]{"RefreshState","StopProxy"});
            Require(failed.LastError == "" && File.ReadAllText(modeFile).Contains("off"), "Disconnected mode selection did not repair its preference");
            await Click(failed, "NavSettings");
            Find<TextBox>(failed, "Listen").Text = listen;
            Find<TextBox>(failed, "Upstream").Text = GuardianRuntime.Text(active, "upstream");
            Find<TextBox>(failed, "CodexConfig").Text = Path.Combine(root, "config.toml");
            Find<TextBox>(failed, "Provider").Text = "missing-provider";
            Find<RetryInput>(failed, "RequestRetries").Value = 3;
            Find<RetryInput>(failed, "StreamRetries").Value = 0;
            Capture(failed, "desktop-startup-error");
            string configBefore = File.ReadAllText(Path.Combine(root, "config.toml"));
            string pending = Path.Combine(root, "responses-guardian.next.exe");
            string checksum = Path.Combine(root, "responses-guardian.next.sha256");
            File.Copy(Path.Combine(root, "responses-guardian.exe"), pending);
            File.WriteAllText(checksum, "bad checksum");
            await Click(failed, "SaveSettings");
            Require(failed.LastError.Contains("checksum") && File.ReadAllText(settingsFile) == "invalid fixture JSON", "Unverified staged program was used for offline validation");
            using (var hash = SHA256.Create())
            using (var input = File.OpenRead(pending))
                File.WriteAllText(checksum, BitConverter.ToString(hash.ComputeHash(input)).Replace("-", ""));
            await Click(failed, "SaveSettings");
            Require(failed.LastError != "" && File.ReadAllText(settingsFile) == "invalid fixture JSON", "Offline invalid provider was saved");
            Find<TextBox>(failed, "Provider").Text = "custom";
            await Click(failed, "SaveSettings");
            Require(failed.LastError == "" && File.ReadAllText(Path.Combine(root, "config.toml")) == configBefore, "Offline recovery changed Codex routing or failed: " + failed.LastError);
            await Click(failed, "ConnectProxy");
            Require(failed.IsConnected && GuardianRuntime.Text(failed.Current, "pid") == pid, "Offline recovery restarted the existing proxy");
            Require(File.Exists(pending), "Offline validation activated the staged proxy");
            failed.Close();
        }
        finally { File.WriteAllText(settingsFile, goodSettings, new UTF8Encoding(false)); }
        var check = new GuardianRuntime(root);
        var result = await Task.Run(() => check.Control("connect"));
        Require(GuardianRuntime.Flag(result, "running") && GuardianRuntime.Text(GuardianRuntime.Map(result, "health"), "pid") == pid, "Closing the failure window stopped the proxy");
        Require(Directory.GetFiles(Path.Combine(root, "runtime"), "desktop-operation-*").Length == 0, "Desktop result files were left behind");
    }

    private static async Task CheckEditing(GuardianForm form)
    {
        var requests=Find<RetryInput>(form,"RequestRetries");
        var streams=Find<RetryInput>(form,"StreamRetries");
        var provider=Find<TextBox>(form,"Provider");
        var save=Find<Button>(form,"SaveSettings");
        Require(!save.Enabled,"Fresh settings should be clean");
        await Click(form,"RequestRetries-Increase");
        Require(requests.Value==21&&save.Enabled,"Styled increment did not update the draft");
        await Click(form,"UndoSettings");
        Require(requests.Value==20&&!save.Enabled&&Find<Button>(form,"RedoSettings").Enabled,"Undo did not restore the saved value");
        await form.Connect();
        Require(Find<Button>(form,"RedoSettings").Enabled,"Status refresh discarded redo history");
        Require(form.HandleHistoryShortcut(Keys.Control|Keys.Y),"Redo shortcut was not consumed");
        Require(requests.Value==21&&save.Enabled,"Ctrl+Y failed");
        form.HandleHistoryShortcut(Keys.Control|Keys.Z);
        form.HandleHistoryShortcut(Keys.Control|Keys.Shift|Keys.Z);
        Require(requests.Value==21,"Ctrl+Shift+Z failed");
        requests.Value=20;
        Require(!save.Enabled,"Manually returning the number to its saved value stayed dirty");

        provider.Text="custom-draft"; streams.Value=25;
        await Click(form,"UndoSettings");
        Require(streams.Value==20&&provider.Text=="custom-draft","Undo did not affect the most recent field only");
        await Click(form,"UndoSettings");
        Require(provider.Text=="custom"&&!save.Enabled,"Undo across fields failed");
        streams.Value=24;
        Require(!Find<Button>(form,"RedoSettings").Enabled,"A new edit did not discard the redo branch");
        streams.Value=20;
        provider.Text=" custom ";
        requests.Text="020";
        Require(!save.Enabled,"Equivalent trimmed settings were marked dirty");
        requests.Text="101";
        Require(!save.Enabled&&Find<Button>(form,"UndoSettings").Enabled,"Out-of-range retries were accepted");
        Capture(form,"desktop-invalid-retries");
        await Click(form,"UndoSettings");
        Require(requests.Text=="020"&&!save.Enabled,"Undo did not restore raw retry text");
        requests.Value=100;
        Require(!Find<Button>(form,"RequestRetries-Increase").Enabled,"Upper retry bound can be exceeded");
        requests.Value=0;
        Require(!Find<Button>(form,"RequestRetries-Decrease").Enabled,"Lower retry bound can be exceeded");
        requests.Value=20; provider.Text="custom";
        Require(!save.Enabled,"Draft did not return to clean state");
        Capture(form,"desktop-restored-settings");
    }

    private static Bitmap ButtonImage(Button button)
    {
        var bitmap=new Bitmap(button.Width,button.Height);
        button.DrawToBitmap(bitmap,new Rectangle(Point.Empty,bitmap.Size));
        return bitmap;
    }
    private static void SameButtonImage(Button button,Bitmap before,string operation)
    {
        using(var after=ButtonImage(button)) {
            for(int y=0;y<before.Height;y++) for(int x=0;x<before.Width;x++) if(before.GetPixel(x,y)!=after.GetPixel(x,y)) {
                before.Save(Path.Combine(captures,operation+"-"+button.Name+"-before.png"),System.Drawing.Imaging.ImageFormat.Png);
                after.Save(Path.Combine(captures,operation+"-"+button.Name+"-during.png"),System.Drawing.Imaging.ImageFormat.Png);
                throw new Exception("Unrelated button changed appearance during "+operation+": "+button.Name);
            }
        }
    }
    private static async Task CheckStableButtons(GuardianForm form,Func<Task> action,string operation,string[] names)
    {
        var before=new Dictionary<Button,Bitmap>(); var initiallyEnabled=new Dictionary<Button,bool>();
        var locked=new Dictionary<Button,int>(); int accidentalClicks=0;
        EventHandler click=delegate { accidentalClicks++; };
        EventHandler change=delegate(object sender,EventArgs e) {
            var button=(Button)sender;
            if(!button.Enabled) {
                Require(form.IsBusy,"Button locked outside an operation"); locked[button]++;
                SameButtonImage(button,before[button],operation);
                button.PerformClick(); // Disabled input must not reach a handler or queue a later action.
            }
        };
        foreach(string name in names) {
            var button=Find<Button>(form,name); before.Add(button,ButtonImage(button));
            initiallyEnabled.Add(button,button.Enabled); locked.Add(button,0);
            button.Click+=click; button.EnabledChanged+=change;
        }
        try {
            Task pending=action();
            await Task.WhenAny(pending,Task.Delay(80));
            // Click helpers may still be awaiting their next poll after the UI has already settled.
            if(form.IsBusy) foreach(var item in before) {
                Require(!item.Key.Enabled,"A slow operation released its input lock early");
                SameButtonImage(item.Key,item.Value,operation);
            }
            await pending;
            Require(!form.IsBusy&&accidentalClicks==0,"Busy input reached an action or the busy guard was not released");
            foreach(var item in before) {
                Require(locked[item.Key]==(initiallyEnabled[item.Key]?1:0),"Expected one temporary lock for "+operation+": "+item.Key.Name);
                Require(item.Key.Enabled==initiallyEnabled[item.Key],"Availability did not recover after "+operation);
                SameButtonImage(item.Key,item.Value,operation+"-finished");
            }
        } finally {
            foreach(var item in before) { item.Key.Click-=click; item.Key.EnabledChanged-=change; item.Value.Dispose(); }
        }
    }
    private static async Task CheckQuietOperations(GuardianForm form)
    {
        var timer=(Timer)typeof(GuardianForm).GetField("timer",BindingFlags.NonPublic|BindingFlags.Instance).GetValue(form);
        bool wasRunning=timer.Enabled; timer.Stop(); Find<Button>(form,"NavOverview").Focus();
        string[] footer={"RefreshState","StopProxy"};
        try {
            await CheckStableButtons(form,()=>Click(form,"Mode-on-error"),"mode-switch",footer);
            Require(GuardianRuntime.Text(form.Current,"mode")=="on-error","Quiet mode switch did not reach the proxy");
            Require(Find<Label>(form,"ModeNotice").Text=="切换后立即生效","Mode feedback did not settle after success");
            Func<Task> poll=async delegate {
                typeof(Timer).GetMethod("OnTick",BindingFlags.NonPublic|BindingFlags.Instance).Invoke(timer,new object[]{EventArgs.Empty});
                await Wait(form);
            };
            await CheckStableButtons(form,poll,"background-refresh",new[]{"RefreshState","StopProxy","Mode-off","Mode-on-error","Mode-always"});
            await CheckStableButtons(form,async delegate {
                await (Task)typeof(GuardianForm).GetMethod("ChangeMode",BindingFlags.NonPublic|BindingFlags.Instance).Invoke(form,new object[]{"invalid-fixture-mode"});
            },"failed-mode-switch",footer);
            Require(form.IsConnected&&form.LastError!="","Failed mode change hid its error or disconnected the proxy");
            string error=form.LastError;
            Require(Find<Label>(form,"ModeNotice").Text=="切换后立即生效","Mode feedback stayed busy after failure");
            await CheckStableButtons(form,poll,"refresh-after-error",footer);
            Require(form.LastError==error,"Automatic refresh cleared an unresolved error");
            await Click(form,"Mode-off");
        } finally { if(wasRunning) timer.Start(); }
    }

    private static SettingsDraft Draft(string provider,int requests)
    { return new SettingsDraft("127.0.0.1:15722","http://127.0.0.1:15721",provider,"config.toml",requests.ToString(),"20"); }
    private static void CheckHistoryReconciliation()
    {
        var history=new SettingsHistory();
        var baseline=Draft("custom",20); var submitted=Draft("custom",21); var newer=Draft("custom-new",21);
        history.Reset(baseline); history.Record(submitted); history.Record(newer);
        history.MarkSaved(submitted,submitted);
        Require(history.Current.SameText(newer)&&history.Dirty,"A save response erased edits made during the save");
        history.Undo();
        Require(history.Current.SameText(submitted)&&!history.Dirty,"Undo did not use the successful save as baseline");
        history.RefreshSaved(submitted);
        Require(history.CanRedo,"An unchanged refresh erased history");
        history.Redo();
        history.RefreshSaved(Draft("external",23));
        Require(history.Current.SameText(newer)&&history.Dirty,"External refresh erased an unsaved draft");
        history.Reset(baseline);
        history.RefreshSaved(submitted);
        Require(history.Current.SameText(submitted)&&!history.Dirty,"Clean settings did not follow an external update");
    }

    private static void CheckPublicSkin(GuardianForm form)
    {
        string[] resources=typeof(GuardianForm).Assembly.GetManifestResourceNames();
        Require(resources.Length==1&&resources[0]=="GuardianIcon","The application embeds unexpected assets");
        Require(!Find<CompanionScene>(form,"Companion").HasPersonalSkin,"Public preview loaded a personal skin");
        Require(PersonalSkin.Load(false)==null,"Public skin policy was ignored");
        string path=Path.Combine(root,"skin-fixture.png");
        using(var source=new Bitmap(2,2)) { source.SetPixel(0,0,Color.Magenta); source.Save(path,System.Drawing.Imaging.ImageFormat.Png); }
        using(var loaded=PersonalSkin.LoadFile(path)) {
            Require(loaded!=null&&loaded.Width==2,"Optional local skin was not loaded");
            File.Move(path,path+".moved"); // The decoder must not keep the user's file locked.
        }
        File.WriteAllText(path,"invalid image fixture");
        Require(PersonalSkin.LoadFile(path)==null,"An invalid personal image did not fall back safely");
    }
    private static int Hit(GuardianForm form,int x,int y)
    {
        long packed=((long)(y&0xffff)<<16)|(uint)(x&0xffff);
        return SendMessage(form.Handle,0x84,IntPtr.Zero,new IntPtr(packed)).ToInt32();
    }
    private static void CheckDpiPainting()
    {
        foreach(int dpi in new[]{96,120,144,192}) {
            float scale=dpi/96F;
            foreach(int width in new[]{470,620}) using(var route=new SignalDisplay {
                Size=new Size((int)(width*scale),(int)(SignalDisplay.MinimumLogicalHeight*scale)),Running=true,
                Listen="127.0.0.1:15722",Upstream="http://[::1]:15721/a-long-address-for-truncation"
            }) {
                for(int node=0;node<3;node++) foreach(bool detail in new[]{false,true}) {
                    var bounds=SignalDisplay.LabelBounds(route.ClientSize,scale,node,detail);
                    Require(route.ClientRectangle.Contains(bounds),"Route text is clipped at DPI "+dpi);
                    if(node<2) Require(bounds.Right<SignalDisplay.LabelBounds(route.ClientSize,scale,node+1,detail).Left,"Adjacent route labels overlap at DPI "+dpi);
                }
                using(var bitmap=new Bitmap(route.Width,route.Height)) {
                    bitmap.SetResolution(dpi,dpi);
                    using(var graphics=Graphics.FromImage(bitmap)) {
                        typeof(SignalDisplay).GetMethod("OnPaint",BindingFlags.NonPublic|BindingFlags.Instance).Invoke(route,new object[]{new PaintEventArgs(graphics,route.ClientRectangle)});
                    }
                    bitmap.Save(Path.Combine(captures,"route-"+dpi+"dpi-"+width+".png"),System.Drawing.Imaging.ImageFormat.Png);
                }
            }
            foreach(var logical in new[]{new Size(266,501),new Size(346,640),new Size(480,501),new Size(346,780)}) {
                var size=new Size((int)(logical.Width*scale),(int)(logical.Height*scale));
                var first=CompanionScene.MeasureGeometry(size,scale,.94F);
                foreach(float aspect in new[]{.55F,1.4F}) {
                    var differentPortrait=CompanionScene.MeasureGeometry(size,scale,aspect);
                    Require(first.Orbit==differentPortrait.Orbit,"Changing the portrait moved the background circle");
                }
                Require(first.Orbit.Top>=106*scale-.1F&&first.Orbit.Bottom<=first.Bubble.Top-10*scale,"Background circle overlaps text at DPI "+dpi);
                Require(first.Orbit.Left>=0&&first.Orbit.Right<=size.Width,"Background circle extends beyond the panel at DPI "+dpi);
                var wider=CompanionScene.MeasureGeometry(new Size(size.Width+(int)(40*scale),size.Height),scale,.94F);
                Require(wider.Orbit.X+wider.Orbit.Width/2>first.Orbit.X+first.Orbit.Width/2,"Background circle did not move with the panel");
            }
        }
    }
    private static async Task CheckWindowLifecycle(GuardianForm form)
    {
        Require(form.FormBorderStyle==FormBorderStyle.Sizable&&form.Region==null,"The window still uses a custom clipped frame");
        Require((GetWindowLongPtr(form.Handle,-16).ToInt64()&0x00040000)!=0,"The native resize frame is missing");
        var b=form.Bounds;
        Require(Hit(form,b.Left+1,b.Top+b.Height/2)==10&&Hit(form,b.Right-2,b.Top+b.Height/2)==11
            &&Hit(form,b.Left+b.Width/2,b.Bottom-2)==15,"Native window edges do not expose resize grips");
        IntPtr handle=form.Handle; Size original=form.Size;
        form.Size=new Size(800,540); await Task.Delay(40); Capture(form,"desktop-small");
        Require(!Find<Panel>(form,"OverviewPage").VerticalScroll.Visible&&!Find<Panel>(form,"OverviewPage").HorizontalScroll.Visible,"Normal overview needs scrolling at the minimum supported size");
        await Click(form,"NavSettings"); await Task.Delay(40); Capture(form,"desktop-small-settings");
        Require(!Find<Panel>(form,"SettingsPage").VerticalScroll.Visible&&!Find<Panel>(form,"SettingsPage").HorizontalScroll.Visible,"Normal settings need scrolling at the minimum supported size");
        await Click(form,"NavOverview");
        form.Size=new Size(1180,760); await Task.Delay(40);
        Require(form.Width==1180&&form.Height==760,"The window could not grow freely");
        form.Size=original;
        await Click(form,"NavSettings");
        Find<TextBox>(form,"Provider").Text="window-lifecycle-draft";
        for(int i=0;i<3;i++) {
            form.WindowState=FormWindowState.Minimized; await Task.Delay(40);
            form.WindowState=FormWindowState.Normal; await Task.Delay(40);
            Require(form.Handle==handle&&form.OwnedForms.Length==0,"Restore recreated a window or left an extra shadow window");
            Require(form.Size==original&&Find<TextBox>(form,"Provider").Text=="window-lifecycle-draft","Restore lost the size or an unsaved draft");
        }
        form.WindowState=FormWindowState.Maximized; await Task.Delay(40);
        form.WindowState=FormWindowState.Normal; await Task.Delay(40);
        Require(form.Size==original&&form.Handle==handle,"Maximize/restore changed the native window identity or size");
        Find<TextBox>(form,"Provider").Text="custom";
        Capture(form,"desktop-restored");
        await Click(form,"NavOverview");
    }

    private static int TopInWindow(Control control,Form window)
    { return control.PointToScreen(Point.Empty).Y-window.Top; }
    private static async Task CheckPageHeightBudget()
    {
        foreach(int dpi in new[]{96,120,144,192}) using(var window=new GuardianForm(Path.Combine(root,"layout-only"),false)) {
            window.ShowInTaskbar=false; window.StartPosition=FormStartPosition.Manual; window.Location=new Point(-3500,-3500);
            window.Show(); await Wait(window,true);
            ((Timer)typeof(GuardianForm).GetField("timer",BindingFlags.NonPublic|BindingFlags.Instance).GetValue(window)).Stop();
            var error=Find<Control>(window,"ErrorCard"); error.Visible=false;
            Find<TextBox>(window,"ErrorDetails").Clear();
            float scale=dpi/96F,factor=dpi/(float)UI.Dpi(window);
            window.Scale(new SizeF(factor,factor)); window.MinimumSize=Size.Empty;
            // Replay physical client dimensions independently of the test machine's native frame.
            // The reported 125% window has a 978x616 client and a 443-pixel page viewport.
            foreach(var size in new[]{
                new Size((int)Math.Round(782.4F*scale),(int)Math.Round(492.8F*scale)),
                new Size((int)Math.Round(782.4F*scale),(int)Math.Round(486.4F*scale)),
                new Size((int)Math.Round(1000*scale),(int)Math.Round(620*scale)),
                new Size((int)Math.Round(782.4F*scale),(int)Math.Round(492.8F*scale))
            }) {
                window.ClientSize=size;
                foreach(string navigation in new[]{"NavOverview","NavSettings"}) {
                    await Click(window,navigation); window.ApplyResponsiveLayout(dpi);
                    bool settings=navigation=="NavSettings";
                    var page=Find<Panel>(window,settings?"SettingsPage":"OverviewPage");
                    Require(!page.VerticalScroll.Visible&&!page.HorizontalScroll.Visible,"Normal page needs scrolling at DPI "+dpi+", client "+size+", page "+navigation);
                    Require(page.Controls[0].Bottom<=page.ClientSize.Height,"Page content is clipped at DPI "+dpi);
                    if(!settings) {
                        var route=Find<SignalDisplay>(window,"SignalRoute");
                        for(int node=0;node<3;node++) Require(route.ClientRectangle.Contains(SignalDisplay.LabelBounds(route.ClientSize,scale,node,true)),"Fitting the page clipped a route address");
                        var count=Find<Label>(window,"RequestCounts");
                        Require(count.Height>=22*scale-1,"Fitting the page compressed the request count text");
                    }
                }
            }
            await Click(window,"NavOverview"); error.Visible=true;
            await Click(window,"NavOverview"); window.ApplyResponsiveLayout(dpi);
            var overflow=Find<Panel>(window,"OverviewPage");
            Require(overflow.VerticalScroll.Visible,"Extra error details became inaccessible instead of scrolling");
            overflow.ScrollControlIntoView(Find<Label>(window,"RequestCounts"));
            Require(overflow.AutoScrollPosition.Y<0,"Overflow content cannot be scrolled into view");
            error.Visible=false; await Click(window,"NavOverview"); window.ApplyResponsiveLayout(dpi);
            Require(!overflow.VerticalScroll.Visible&&overflow.Controls[0].Top==0,"Clearing an error left a stale scrollbar or offset");
            window.Close();
        }
    }
    private static async Task CheckLiveResize(GuardianForm form)
    {
        Size original=form.Size;
        foreach(string page in new[]{"NavOverview","NavSettings"}) {
            await Click(form,page);
            var before=new Dictionary<string,Rectangle>();
            SendMessage(form.Handle,0x231,IntPtr.Zero,IntPtr.Zero);
            for(int step=0;step<20;step++) {
                form.ClientSize=new Size(900+step*4,540+step*3);
                Application.DoEvents();
            }
            SendMessage(form.Handle,0x232,IntPtr.Zero,IntPtr.Zero);
            foreach(string name in new[]{"SignalRoute","ConnectionNote","RetryNote","ConnectProxy"}) before[name]=Find<Control>(form,name).Bounds;
            // The settled layout must match an ordinary resize to exactly the same size.
            Size settled=form.ClientSize; form.ClientSize=new Size(settled.Width+1,settled.Height+1); form.ClientSize=settled;
            foreach(var pair in before) Require(Find<Control>(form,pair.Key).Bounds==pair.Value,"Live resize left a stale layout: "+pair.Key);
            var viewport=Find<Panel>(form,page=="NavOverview"?"OverviewPage":"SettingsPage");
            Require(!viewport.VerticalScroll.Visible&&!viewport.HorizontalScroll.Visible,"Live resize introduced unnecessary scrolling");
        }
        form.Size=original; await Click(form,"NavOverview");
    }
    private static async Task CheckFluidLayout(GuardianForm form)
    {
        Size original=form.Size;
        var route=Find<SignalDisplay>(form,"SignalRoute");
        int previous=-1;
        for(int height=612;height<=628;height++) {
            form.ClientSize=new Size(1000,height); form.PerformLayout();
            int top=TopInWindow(route,form);
            if(previous>=0) Require(Math.Abs(top-previous)<=8,"Overview jumps when crossing the old compact-layout boundary");
            previous=top;
        }
        Capture(form,"desktop-fluid-overview");
        await Click(form,"NavSettings");
        var note=Find<Label>(form,"ConnectionNote"); var retryNote=Find<Label>(form,"RetryNote");
        string noteText=note.Text,retryText=retryNote.Text; previous=-1;
        for(int width=910;width<=930;width++) {
            form.ClientSize=new Size(width,610); form.PerformLayout();
            int top=TopInWindow(note,form);
            Require(note.Visible&&note.Text==noteText&&retryNote.Text==retryText,"Resizing changed or hid settings guidance");
            if(previous>=0) Require(Math.Abs(top-previous)<=8,"Settings jump when crossing the old width boundary");
            previous=top;
        }
        Require(!Find<Button>(form,"ConnectProxy").Enabled,"Settings page re-enabled the connected proxy button");
        form.Size=new Size(890,570); Capture(form,"desktop-fluid-settings");
        Require(!Find<Panel>(form,"SettingsPage").VerticalScroll.Visible,"Settings overflow at the reported narrow size");
        foreach(string name in new[]{"Listen","Upstream","Provider","CodexConfig","RequestRetries","StreamRetries","BrowseConfig"}) {
            Control control=Find<Control>(form,name);
            if(control.Parent is InputSurface) control=control.Parent;
            Require(control.Parent.ClientRectangle.Contains(control.Bounds),"A settings control is clipped by its row: "+name);
        }
        await Click(form,"NavOverview");
        foreach(var size in new[]{new Size(800,540),new Size(1000,680),new Size(1430,540),new Size(1050,820)}) {
            await Click(form,"NavSettings");
            form.Size=size; form.PerformLayout();
            Require(!Find<Panel>(form,"SettingsPage").VerticalScroll.Visible,"Settings kept a stale scroll extent after resizing");
            await Click(form,"NavOverview");
            Require(!Find<Panel>(form,"OverviewPage").VerticalScroll.Visible,"A hidden page kept a stale scroll extent after shrinking");
            var scene=Find<CompanionScene>(form,"Companion");
            var layout=scene.Geometry;
            float scale=UI.Dpi(form)/96F;
            for(int node=0;node<3;node++) Require(route.ClientRectangle.Contains(SignalDisplay.LabelBounds(route.ClientSize,scale,node,true)),"Route address is clipped at the current window size");
            Require(layout.Orbit.Left>=0&&layout.Orbit.Right<=scene.Width,"Background circle extends beyond the side panel");
            Require(layout.Orbit.Top>=100&&layout.Orbit.Bottom<layout.Bubble.Top-10,"Background circle overlaps the greeting or speech bubble");
            Require(Math.Abs(layout.Orbit.Width-layout.Orbit.Height)<.1,"Background circle is distorted");
        }
        form.Size=new Size(1430,540); Capture(form,"desktop-wide-short");
        form.Size=original;
    }
}
