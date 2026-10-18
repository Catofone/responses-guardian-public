using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Reflection;
using System.Threading.Tasks;
using System.Windows.Forms;

namespace ResponsesGuardian.Desktop
{
    internal sealed class GuardianForm : Form
    {
        private readonly GuardianRuntime runtime;
        private readonly Label status = new Label();
        private readonly Label hint = new Label();
        private readonly Label pending = new Label();
        private readonly Label formState = new Label();
        private readonly Label counts = new Label();
        private readonly Label summary = new Label();
        private readonly Label modeNotice = new Label();
        private readonly TextBox listen = new TextBox();
        private readonly TextBox upstream = new TextBox();
        private readonly TextBox provider = new TextBox();
        private readonly TextBox config = new TextBox();
        private readonly RetryInput requestRetries = new RetryInput();
        private readonly RetryInput streamRetries = new RetryInput();
        private readonly TextBox errorBox = new TextBox();
        private readonly ModernButton connect = new ModernButton();
        private readonly ModernButton refresh = new ModernButton();
        private readonly ModernButton stop = new ModernButton();
        private readonly ModernButton save = new ModernButton();
        private readonly ModernButton navOverview = new ModernButton();
        private readonly ModernButton navSettings = new ModernButton();
        private readonly ModernButton undoSettings = new ModernButton();
        private readonly ModernButton redoSettings = new ModernButton();
        private readonly SettingsHistory edits = new SettingsHistory();
        private readonly ToolTip tips = new ToolTip();
        private readonly StatusPill pill = new StatusPill();
        private readonly SignalDisplay signal = new SignalDisplay();
        private readonly CompanionScene companion;
        private readonly Dictionary<string, ModeButton> modes = new Dictionary<string, ModeButton>();
        private readonly System.Windows.Forms.Timer timer = new System.Windows.Forms.Timer();
        private Surface errorCard;
        private Panel overviewPage;
        private Panel settingsPage;
        private Panel primaryActions;
        private Panel secondaryActions;
        private Panel footerMessage;
        private Label closeNote;
        private TableLayoutPanel deskLayout,bodyLayout,footerLayout,choicesLayout,modeHeadingLayout,detailsLayout,settingsHeadingLayout,retriesLayout;
        private TableLayoutPanel overviewContent,settingsContent;
        private Panel routeRule,settingsRule;
        private Label routeHeading,connectionNote,retryHeading,retryNote;
        private readonly List<Control> connectionRows=new List<Control>();
        private bool applyingLayout;
        private Size lastLayoutSize;
        private int lastLayoutDpi;
        private bool interactiveResize;
        private long lastLayoutTimestamp;
        private Dictionary<string, object> current;
        private bool filling;
        private bool settingsError;
        private bool restartRequired;
        private enum BusyFeedback { Normal, ModeSwitch, BackgroundRefresh }
        private BusyFeedback busyFeedback;
        private bool dirty { get { return edits.Dirty; } }
        internal bool IsBusy { get; private set; }
        internal bool IsConnected { get; private set; }
        internal string LastError { get { return errorBox.Text; } }
        internal Dictionary<string, object> Current { get { return current; } }

        internal GuardianForm(string root,bool usePersonalSkin=true)
        {
            runtime = new GuardianRuntime(root);
            companion = new CompanionScene(usePersonalSkin);
            Text = "Guardian"; Name = "GuardianManager";
            Font = UI.Font(10F); ForeColor = UI.Ink; BackColor = UI.Background;
            FormBorderStyle = FormBorderStyle.Sizable; DoubleBuffered = true; ResizeRedraw = true;
            KeyPreview = true;
            ClientSize = new Size(1020, 640); MinimumSize = new Size(800, 540);
            AutoScaleDimensions = new SizeF(96F, 96F); AutoScaleMode = AutoScaleMode.Dpi;
            StartPosition = FormStartPosition.CenterScreen;
            Icon = Icon.ExtractAssociatedIcon(Assembly.GetExecutingAssembly().Location);
            BuildWindow(); FillSettings(runtime.Preferences());
            ApplyResponsiveLayout();
            timer.Interval = 5000;
            timer.Tick += async delegate { if (!IsBusy && IsConnected && !interactiveResize) await RefreshState(false); };
            Shown += async delegate { timer.Start(); await Connect(); };
            FormClosed += delegate { timer.Stop(); timer.Dispose(); tips.Dispose(); };
            KeyDown += delegate(object sender,KeyEventArgs e) { if(HandleHistoryShortcut(e.KeyData)) { e.Handled=true; e.SuppressKeyPress=true; } };
        }

        protected override bool ProcessCmdKey(ref Message message,Keys keys)
        { return HandleHistoryShortcut(keys)||base.ProcessCmdKey(ref message,keys); }
        internal bool HandleHistoryShortcut(Keys keys)
        {
            if(settingsPage==null||!settingsPage.Visible) return false;
            if(keys==(Keys.Control|Keys.Z)) { TravelHistory(false); return true; }
            if(keys==(Keys.Control|Keys.Y)||keys==(Keys.Control|Keys.Shift|Keys.Z)) { TravelHistory(true); return true; }
            return false;
        }
        protected override void OnLoad(EventArgs e)
        {
            base.OnLoad(e);
            var area = Screen.FromControl(this).WorkingArea;
            MinimumSize = new Size(Math.Min(MinimumSize.Width, area.Width - 24), Math.Min(MinimumSize.Height, area.Height - 24));
            Size = new Size(Math.Min(Width, area.Width - 24), Math.Min(Height, area.Height - 24));
            lastLayoutSize=Size.Empty;
            ApplyResponsiveLayout();
        }
        protected override void OnClientSizeChanged(EventArgs e) { base.OnClientSizeChanged(e); ApplyResponsiveLayout(); }
        protected override void OnResizeBegin(EventArgs e)
        {
            interactiveResize=true; lastLayoutTimestamp=0; companion.SetInteractiveResize(true);
            base.OnResizeBegin(e);
        }
        protected override void OnResizeEnd(EventArgs e)
        {
            interactiveResize=false; ApplyResponsiveLayout(); companion.SetInteractiveResize(false);
            base.OnResizeEnd(e);
        }
        private void ApplyResponsiveLayout()
        { ApplyResponsiveLayout(UI.Dpi(this)); }
        internal void ApplyResponsiveLayout(int dpi)
        {
            if(applyingLayout||retryNote==null) return;
            if(lastLayoutSize==ClientSize&&lastLayoutDpi==dpi) return;
            long now=Stopwatch.GetTimestamp();
            if(interactiveResize&&lastLayoutTimestamp!=0&&(now-lastLayoutTimestamp)*1000.0/Stopwatch.Frequency<16) return;
            float s=dpi/96F;
            float room=Math.Max(0,Math.Min(1,(ClientSize.Height/s-500)/180F));
            room=room*room*(3-2*room);
            float widthRoom=Math.Max(0,Math.Min(1,(ClientSize.Width/s-784)/236F));
            Func<float,float,int> gap=(small,large)=>(int)Math.Round((small+(large-small)*room)*s);
            Func<int,int> px=value=>(int)Math.Round(value*s);
            bool settings=navSettings.Checked;
            var layouts=new List<Control> { this,deskLayout,bodyLayout,footerLayout };
            if(settings) {
                layouts.AddRange(new Control[]{settingsPage,settingsContent,settingsHeadingLayout,retriesLayout});
                layouts.AddRange(connectionRows);
            } else layouts.AddRange(new Control[]{overviewPage,overviewContent,modeHeadingLayout,choicesLayout,detailsLayout});
            applyingLayout=true;
            foreach(var layout in layouts) layout.SuspendLayout();
            try {
                int horizontal=(int)Math.Round((20+8*widthRoom)*s);
                deskLayout.Padding=new Padding(horizontal,gap(14,22),horizontal,gap(10,16));
                SetRowHeight(deskLayout.RowStyles[0],gap(36,42)); SetRowHeight(deskLayout.RowStyles[2],gap(64,76));
                bodyLayout.Padding=new Padding(0,gap(14,23),0,0);
                footerLayout.Padding=new Padding(0,gap(4,8),0,0); SetRowHeight(footerLayout.RowStyles[0],gap(36,39));
                if(!settings) {
                    modeHeadingLayout.Height=gap(24,28); choicesLayout.Height=gap(62,79);
                    hint.Height=gap(40,48); hint.Margin=new Padding(0,gap(8,10),0,gap(14,24));
                    hint.Padding=new Padding(px(12),gap(7,10),px(10),px(4));
                    routeHeading.Height=gap(24,29); signal.Height=gap(SignalDisplay.MinimumLogicalHeight,132); signal.Margin=new Padding(0,0,0,gap(8,12));
                    detailsLayout.Height=gap(22,25); routeRule.Margin=new Padding(0,gap(4,6),0,gap(8,12)); counts.Height=gap(22,26);
                } else {
                    settingsHeadingLayout.Height=gap(30,36); settingsHeadingLayout.Margin=new Padding(0,0,0,gap(8,14));
                    foreach(Control row in connectionRows) {
                        row.Height=gap(36,48);
                        foreach(Control field in row.Controls) {
                            field.Margin=new Padding(field.Margin.Left,0,field.Margin.Right,gap(6,10));
                            var surface=field as InputSurface;
                            if(surface!=null) surface.Padding=new Padding(px(8),gap(5,7),px(8),gap(4,5));
                        }
                    }
                    connectionNote.Height=gap(22,26);
                    connectionNote.Margin=new Padding(px(104),0,0,gap(6,10));
                    settingsRule.Margin=new Padding(0,gap(8,12),0,gap(12,18));
                    retryHeading.Margin=new Padding(0,0,0,gap(8,12));
                    retriesLayout.Height=gap(38,44);
                    retryNote.Height=gap(24,26); retryNote.Margin=new Padding(px(104),px(4),0,gap(0,8));
                }
                UpdateContentSizes();
                lastLayoutSize=ClientSize; lastLayoutDpi=dpi;
                lastLayoutTimestamp=Stopwatch.GetTimestamp();
            } finally {
                // Lay out parents first; children then settle once at their final size.
                // Keep the viewport suspended until fitting finishes, so a transient oversized
                // stack cannot flash a scrollbar during continuous resizing.
                var page=settings?settingsPage:overviewPage;
                try {
                    foreach(var layout in layouts) if(layout!=page) layout.ResumeLayout(true);
                    FitPageContents(settings,dpi);
                } finally { page.ResumeLayout(true); applyingLayout=false; }
            }
        }

        private void FitPageContents(bool settings,int dpi)
        {
            var page=settings?settingsPage:overviewPage;
            var content=settings?settingsContent:overviewContent;
            // Native frame sizes and rounding vary with DPI/theme. Use the settled viewport,
            // keeping text/card heights intact and reclaiming only flexible whitespace.
            int excess=content.Height+2-page.ClientSize.Height;
            if(excess<=0) return;
            float scale=dpi/96F;
            Func<int,int> px=value=>(int)Math.Round(value*scale);
            content.SuspendLayout();
            try {
                if(settings) {
                    TrimVerticalMargin(settingsHeadingLayout,0,px(4),ref excess);
                    TrimVerticalMargin(connectionNote,0,px(2),ref excess);
                    TrimVerticalMargin(settingsRule,px(2),px(4),ref excess);
                    TrimVerticalMargin(retryHeading,0,px(4),ref excess);
                    TrimVerticalMargin(retryNote,px(2),0,ref excess);
                } else {
                    TrimVerticalMargin(hint,px(4),px(6),ref excess);
                    TrimVerticalMargin(signal,0,px(4),ref excess);
                    TrimVerticalMargin(routeRule,px(2),px(4),ref excess);
                }
                UpdateContentSizes();
            } finally { content.ResumeLayout(true); }
            if(excess>0) {
                var padding=bodyLayout.Padding;
                padding.Top-=Math.Min(excess,Math.Max(0,padding.Top-px(6)));
                bodyLayout.Padding=padding;
                bodyLayout.PerformLayout();
            }
            page.PerformLayout();
        }
        private static void TrimVerticalMargin(Control control,int top,int bottom,ref int excess)
        {
            if(excess<=0) return;
            var margin=control.Margin;
            int take=Math.Min(excess,Math.Max(0,margin.Bottom-bottom));
            margin.Bottom-=take; excess-=take;
            take=Math.Min(excess,Math.Max(0,margin.Top-top));
            margin.Top-=take; excess-=take;
            control.Margin=margin;
        }

        private void BuildWindow()
        {
            var shell = Table(1); shell.Dock=DockStyle.Fill; shell.RowCount=1; shell.BackColor=Color.White;
            shell.ColumnStyles.Add(new ColumnStyle(SizeType.Percent,100)); shell.RowStyles.Add(new RowStyle(SizeType.Percent,100)); Controls.Add(shell);

            var split = Table(2); split.Dock = DockStyle.Fill; split.RowCount = 1; split.RowStyles.Add(new RowStyle(SizeType.Percent, 100));
            split.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 34)); split.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 66)); shell.Controls.Add(split, 0, 0);
            companion.Name = "Companion"; companion.Dock = DockStyle.Fill; companion.Margin = new Padding(0); split.Controls.Add(companion, 0, 0);
            var desk = Table(1); desk.Dock = DockStyle.Fill; desk.Padding = new Padding(28, 22, 28, 16); desk.RowCount = 3; desk.BackColor = Color.White;
            deskLayout=desk;
            desk.ColumnStyles.Add(new ColumnStyle(SizeType.Percent,100)); desk.RowStyles.Add(new RowStyle(SizeType.Absolute, 42)); desk.RowStyles.Add(new RowStyle(SizeType.Percent,100)); desk.RowStyles.Add(new RowStyle(SizeType.Absolute, 76)); split.Controls.Add(desk, 1, 0);
            var tabs = new Panel { Dock = DockStyle.Fill, Margin = new Padding(0, 0, 0, 6) };
            navOverview.Name = "NavOverview"; navOverview.Text = "工作台"; navOverview.Navigation = true; navOverview.Dock = DockStyle.Left; navOverview.Width = 103;
            navSettings.Name = "NavSettings"; navSettings.Text = "连接设置"; navSettings.Navigation = true; navSettings.Dock = DockStyle.Left; navSettings.Width = 108;
            navOverview.Click += delegate { ShowPage(false); }; navSettings.Click += delegate { ShowPage(true); };
            pill.Dock = DockStyle.Right; pill.Width = 122;
            tabs.Controls.Add(pill); tabs.Controls.Add(navSettings); tabs.Controls.Add(navOverview); desk.Controls.Add(tabs, 0, 0);

            var body = Table(1); body.Dock = DockStyle.Fill; body.Padding = new Padding(0, 23, 0, 0); body.RowCount = 2;
            bodyLayout=body;
            body.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100)); body.RowStyles.Add(new RowStyle(SizeType.AutoSize)); body.RowStyles.Add(new RowStyle(SizeType.Percent, 100)); desk.Controls.Add(body, 0, 1);
            errorCard = new Surface { Name = "ErrorCard", Fill = Color.FromArgb(252, 237, 227), BackColor = Color.FromArgb(252, 237, 227), Border = Color.FromArgb(218, 167, 138), Height = 95, Dock = DockStyle.Fill, Padding = new Padding(12, 10, 12, 10), Margin = new Padding(0, 0, 0, 14), Visible = false };
            errorBox.Name = "ErrorDetails"; errorBox.Multiline = true; errorBox.ReadOnly = true; errorBox.ScrollBars = ScrollBars.Vertical; errorBox.BorderStyle = BorderStyle.None; errorBox.BackColor = errorCard.Fill; errorBox.ForeColor = Color.FromArgb(146, 65, 35); errorBox.Dock = DockStyle.Fill; errorBox.Font = UI.Font(9F); errorCard.Controls.Add(errorBox); body.Controls.Add(errorCard, 0, 0);
            var pages = new Panel { Dock = DockStyle.Fill, Margin = new Padding(0) }; body.Controls.Add(pages, 0, 1);
            overviewPage = new Panel { Dock = DockStyle.Fill, AutoScroll = true, BackColor = Color.White, Name = "OverviewPage" };
            settingsPage = new Panel { Dock = DockStyle.Fill, AutoScroll = true, BackColor = Color.White, Name = "SettingsPage" };
            pages.Controls.Add(overviewPage); pages.Controls.Add(settingsPage); BuildOverview(); BuildSettings();

            var footer = Table(4); footer.Dock = DockStyle.Fill; footer.Padding = new Padding(0, 8, 0, 0); footer.RowCount = 2;
            footerLayout=footer;
            footer.RowStyles.Add(new RowStyle(SizeType.Absolute, 39)); footer.RowStyles.Add(new RowStyle(SizeType.Percent, 100));
            footer.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 144)); footer.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 110)); footer.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100)); footer.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 96));
            connect.Name = "ConnectProxy"; connect.Text = "连接代理"; connect.Primary = true; connect.Dock = DockStyle.Fill; connect.Click += async delegate { await Connect(); };
            refresh.Name = "RefreshState"; refresh.Text = "刷新"; refresh.Dock = DockStyle.Fill; refresh.Click += async delegate { await RefreshState(true); };
            stop.Name = "StopProxy"; stop.Text = "停止代理"; stop.Danger = true; stop.Dock = DockStyle.Fill; stop.Margin = new Padding(0); stop.Click += async delegate { await StopProxy(); };
            primaryActions = new Panel { Dock = DockStyle.Fill, Margin = new Padding(0, 0, 9, 0) }; secondaryActions = new Panel { Dock = DockStyle.Fill, Margin = new Padding(0, 0, 9, 0) }; footerMessage = new Panel { Dock = DockStyle.Fill, Margin = new Padding(10, 0, 12, 0) };
            footerMessage.Margin = new Padding(0, 5, 0, 0);
            closeNote = new Label { Text = "关闭窗口后，代理继续运行。", ForeColor = UI.Muted, Font = UI.Font(8F), Dock = DockStyle.Fill, TextAlign = ContentAlignment.MiddleLeft };
            footer.Controls.Add(primaryActions, 0, 0); footer.Controls.Add(secondaryActions, 1, 0); footer.Controls.Add(stop, 3, 0);
            footer.Controls.Add(footerMessage, 0, 1); footer.SetColumnSpan(footerMessage, 4); desk.Controls.Add(footer, 0, 2);
            foreach (var field in new TextBox[] { listen, upstream, provider, config }) field.TextChanged += delegate { RecordEdit(); };
            requestRetries.ValueChanged += delegate { RecordEdit(); }; streamRetries.ValueChanged += delegate { RecordEdit(); };
            ShowPage(false); UpdateEnabled();
        }

        private void BuildOverview()
        {
            var content = Stack(); overviewContent=content; overviewPage.Controls.Add(content);
            var modeHeading = Table(2); modeHeading.Dock = DockStyle.Top; modeHeading.Height = 28; modeHeading.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100)); modeHeading.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 140));
            modeHeadingLayout=modeHeading;
            modeHeading.Controls.Add(new Label { Text = "请求处理", Font = UI.Font(10F, FontStyle.Bold), Dock = DockStyle.Fill }, 0, 0);
            modeNotice.Name="ModeNotice"; modeNotice.Text="切换后立即生效"; modeNotice.ForeColor=UI.Muted; modeNotice.Font=UI.Font(8.5F); modeNotice.Dock=DockStyle.Fill; modeNotice.TextAlign=ContentAlignment.TopRight;
            modeHeading.Controls.Add(modeNotice,1,0); content.Controls.Add(modeHeading);
            var choices = SingleRow(Table(3)); choices.Dock = DockStyle.Top; choices.Height = 79;
            choicesLayout=choices;
            foreach (string mode in new[] { "off", "on-error", "always" })
            {
                choices.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100F/3));
                var button = new ModeButton { Name = "Mode-" + mode, Code = mode, Title = mode == "off" ? "纯转发" : mode == "on-error" ? "出错后恢复" : "每次清理", Dock = DockStyle.Fill, Margin = new Padding(0, 0, mode == "always" ? 0 : 8, 0) };
                button.Text = button.Title; button.AccessibleName = button.Title + " " + mode; string selected = mode; button.Click += async delegate { await ChangeMode(selected); }; modes.Add(mode, button); choices.Controls.Add(button);
            }
            content.Controls.Add(choices);
            hint.Text = "完整保留请求与历史。"; hint.Dock = DockStyle.Top; hint.Height = 48; hint.Padding = new Padding(12, 10, 10, 6); hint.BackColor = UI.Tint; hint.ForeColor = UI.Muted; hint.Font = UI.Font(8.7F); hint.Margin = new Padding(0, 10, 0, 24); content.Controls.Add(hint);
            routeHeading=new Label { Text = "连接线路", Font = UI.Font(10F, FontStyle.Bold), Height = 29, Dock = DockStyle.Top, Margin = new Padding(0) }; content.Controls.Add(routeHeading);
            signal.Name = "SignalRoute"; signal.Dock = DockStyle.Top; signal.Height = 126; signal.Margin = new Padding(0, 0, 0, 12); content.Controls.Add(signal);
            var details = Table(2); details.Dock = DockStyle.Top; details.Height = 25; details.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 58)); details.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 42));
            detailsLayout=details;
            summary.Name = "ActiveSummary"; summary.Text = "提供商 —"; summary.Font = UI.Font(8.7F); summary.ForeColor = UI.Muted; summary.Dock = DockStyle.Fill;
            status.Name = "StatusText"; status.Text = "正在连接…"; status.Font = new Font("Consolas", 9F); status.ForeColor = UI.Muted; status.Dock = DockStyle.Fill; status.TextAlign = ContentAlignment.TopRight;
            details.Controls.Add(summary, 0, 0); details.Controls.Add(status, 1, 0); content.Controls.Add(details);
            var separator = new Panel { Height = 1, Dock = DockStyle.Top, BackColor = UI.Line, Margin = new Padding(0, 6, 0, 12) }; content.Controls.Add(separator);
            routeRule=separator;
            counts.Name = "RequestCounts"; counts.Text = "本次运行    转发 —    清理 —    恢复 —    失败 —"; counts.ForeColor = UI.Muted; counts.Font = UI.Font(9F); counts.Height = 26; counts.Dock = DockStyle.Top; content.Controls.Add(counts);
        }

        private void BuildSettings()
        {
            var content = Stack(); settingsContent=content; settingsPage.Controls.Add(content);
            var heading=Table(3); heading.Dock=DockStyle.Top; heading.Height=35; heading.Margin=new Padding(0,0,0,10);
            settingsHeadingLayout=heading;
            heading.RowCount=1; heading.RowStyles.Add(new RowStyle(SizeType.Percent,100));
            heading.ColumnStyles.Add(new ColumnStyle(SizeType.Percent,100)); heading.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute,82)); heading.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute,82));
            heading.Controls.Add(new Label { Text="连接配置",Font=UI.Font(10.5F,FontStyle.Bold),Dock=DockStyle.Fill,TextAlign=ContentAlignment.MiddleLeft },0,0);
            undoSettings.Name="UndoSettings"; undoSettings.Text="↶ 撤销"; undoSettings.Dock=DockStyle.Fill; undoSettings.Margin=new Padding(0,0,8,0);
            redoSettings.Name="RedoSettings"; redoSettings.Text="↷ 重做"; redoSettings.Dock=DockStyle.Fill; redoSettings.Margin=new Padding(0);
            undoSettings.Click+=delegate { TravelHistory(false); }; redoSettings.Click+=delegate { TravelHistory(true); };
            tips.SetToolTip(undoSettings,"撤销上一步 · Ctrl+Z"); tips.SetToolTip(redoSettings,"重做 · Ctrl+Y / Ctrl+Shift+Z");
            heading.Controls.Add(undoSettings,1,0); heading.Controls.Add(redoSettings,2,0); content.Controls.Add(heading);
            listen.Name = "Listen"; upstream.Name = "Upstream"; provider.Name = "Provider"; config.Name = "CodexConfig"; provider.MaxLength = 64;
            var listenRow=Setting("监听地址",listen,"Guardian 的本地地址"); var upstreamRow=Setting("CCSwitch",upstream,"本地上游地址"); var providerRow=Setting("提供商 ID",provider,"例如 custom");
            connectionRows.Add(listenRow); connectionRows.Add(upstreamRow); connectionRows.Add(providerRow);
            content.Controls.Add(listenRow); content.Controls.Add(upstreamRow); content.Controls.Add(providerRow);
            var pathRow = SingleRow(Table(3)); pathRow.Height = 47; pathRow.Dock = DockStyle.Top; pathRow.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 104)); pathRow.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100)); pathRow.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 78));
            connectionRows.Add(pathRow);
            pathRow.Controls.Add(FieldLabel("Codex 配置"), 0, 0); pathRow.Controls.Add(new InputSurface(config) { Dock = DockStyle.Fill, Margin = new Padding(0, 0, 8, 10) }, 1, 0);
            var browse = new ModernButton { Text = "选择…", Name = "BrowseConfig", Dock = DockStyle.Fill, Margin = new Padding(0, 0, 0, 10) };
            browse.Click += delegate { using(var dialog=new OpenFileDialog { Filter="Codex 配置 (*.toml)|*.toml", CheckFileExists=true, Title="选择 Codex 配置文件" }) if(dialog.ShowDialog(this)==DialogResult.OK) config.Text=dialog.FileName; };
            pathRow.Controls.Add(browse, 2, 0); content.Controls.Add(pathRow);
            connectionNote=new Label { Name="ConnectionNote", Text = "连接配置在下次启动代理时生效。", ForeColor = UI.Muted, Font = UI.Font(8.5F), Height = 26, Dock = DockStyle.Top, Margin = new Padding(104, 0, 0, 10) }; content.Controls.Add(connectionNote);
            var divider = new Panel { Height = 1, Dock = DockStyle.Top, BackColor = UI.Line, Margin = new Padding(0, 0, 0, 16) }; content.Controls.Add(divider);
            settingsRule=divider;
            retryHeading=new Label { Text = "重试", Font = UI.Font(10.5F, FontStyle.Bold), AutoSize = true, Margin = new Padding(0, 0, 0, 14) }; content.Controls.Add(retryHeading);
            requestRetries.Name = "RequestRetries"; streamRetries.Name = "StreamRetries";
            var retries = SingleRow(Table(4)); retries.Height = 44; retries.Dock = DockStyle.Top; retries.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 104)); retries.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 50)); retries.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 90)); retries.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 50));
            retriesLayout=retries;
            requestRetries.Dock=streamRetries.Dock=DockStyle.Fill; requestRetries.Margin=new Padding(0,0,22,6); streamRetries.Margin=new Padding(0,0,0,6);
            retries.Controls.Add(FieldLabel("请求失败"),0,0); retries.Controls.Add(requestRetries,1,0); retries.Controls.Add(FieldLabel("流中断"),2,0); retries.Controls.Add(streamRetries,3,0); content.Controls.Add(retries);
            retryNote=new Label { Name="RetryNote", Text = "0–100 次，0 表示不重试；保存后重启 Codex 生效。", ForeColor = UI.Muted, Font = UI.Font(8.5F), Height = 26, Dock = DockStyle.Top, Margin = new Padding(104, 4, 0, 8) }; content.Controls.Add(retryNote);
            pending.Name = "PendingRestart"; pending.Text = "已保存新的地址。退出 Codex 后，重启代理，再打开 Codex。"; pending.ForeColor = UI.Warning; pending.BackColor = Color.FromArgb(255,248,228); pending.Padding = new Padding(10); pending.Height = 56; pending.Dock = DockStyle.Top; pending.Visible = false; pending.Margin = new Padding(0, 0, 0, 8); content.Controls.Add(pending);
            formState.Text = "设置已保存"; formState.ForeColor = UI.Muted; formState.Font = UI.Font(8.5F); formState.Dock = DockStyle.Fill; formState.TextAlign = ContentAlignment.MiddleLeft;
            save.Text = "保存"; save.Name = "SaveSettings"; save.Primary = true; save.Dock = DockStyle.Fill; save.Click += async delegate { await SaveSettings(); };
        }
        private static Control Setting(string label, Control editor, string help)
        {
            var row = SingleRow(Table(2)); row.Dock = DockStyle.Top; row.Height = 47; row.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 104)); row.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100));
            row.Controls.Add(FieldLabel(label), 0, 0); row.Controls.Add(new InputSurface(editor) { Dock = DockStyle.Fill, Margin = new Padding(0, 0, 0, 10) }, 1, 0); editor.AccessibleDescription = help; return row;
        }
        private static Label FieldLabel(string text) { return new Label { Text = text, Font = UI.Font(9.5F), Dock = DockStyle.Fill, TextAlign = ContentAlignment.MiddleLeft, Margin = new Padding(0, 0, 0, 10) }; }
        private static TableLayoutPanel Table(int columns) { return new SmoothTable { ColumnCount = columns, Margin = new Padding(0), BackColor = Color.White }; }
        private static TableLayoutPanel SingleRow(TableLayoutPanel table) { table.RowCount=1; table.RowStyles.Add(new RowStyle(SizeType.Percent,100)); return table; }
        private static TableLayoutPanel Stack() { var table=Table(1); table.Dock=DockStyle.Top; table.ColumnStyles.Add(new ColumnStyle(SizeType.Percent,100)); return table; }
        private static void SetRowHeight(RowStyle row,float height) { if(row.Height!=height) row.Height=height; }
        private void UpdateContentSizes()
        {
            var stack=navSettings.Checked?settingsContent:overviewContent;
            if(stack==null) return;
            stack.SuspendLayout();
            try {
                stack.RowCount=stack.Controls.Count;
                while(stack.RowStyles.Count<stack.RowCount) stack.RowStyles.Add(new RowStyle(SizeType.Absolute,0));
                int total=0,row=0;
                foreach(Control control in stack.Controls) {
                    if(stack.GetRow(control)!=row) stack.SetRow(control,row);
                    int height=(control.AutoSize?control.PreferredSize.Height:control.Height)+control.Margin.Vertical;
                    // A previously collapsed row must recover its intended height before layout.
                    if(control==pending) {
                        pending.Height=(int)Math.Round(56*UI.Dpi(this)/96F);
                        height=restartRequired?pending.Height+pending.Margin.Vertical:0;
                    }
                    SetRowHeight(stack.RowStyles[row],height); total+=height; row++;
                }
                if(stack.Height!=total) stack.Height=total;
            } finally { stack.ResumeLayout(false); }
        }
        private void ShowPage(bool settings)
        {
            overviewPage.Visible=!settings; settingsPage.Visible=settings; navOverview.Checked=!settings; navSettings.Checked=settings; navOverview.Invalidate(); navSettings.Invalidate();
            primaryActions.Controls.Clear(); secondaryActions.Controls.Clear(); footerMessage.Controls.Clear();
            if(settings) { primaryActions.Controls.Add(save); secondaryActions.Controls.Add(connect); footerMessage.Controls.Add(formState); }
            else { primaryActions.Controls.Add(connect); secondaryActions.Controls.Add(refresh); footerMessage.Controls.Add(closeNote); }
            lastLayoutSize=Size.Empty; lastLayoutTimestamp=0;
            ApplyResponsiveLayout();
            UpdateContentSizes();
            if(settings) settingsContent.PerformLayout(); else overviewContent.PerformLayout();
        }
        private SettingsDraft ReadDraft()
        { return new SettingsDraft(listen.Text,upstream.Text,provider.Text,config.Text,requestRetries.Text,streamRetries.Text); }
        private void RecordEdit()
        {
            if(filling) return;
            edits.Record(ReadDraft()); ClearSettingsError(); UpdateEnabled();
        }
        private void ClearSettingsError()
        { if(settingsError) { errorBox.Clear(); errorCard.Visible=false; settingsError=false; } }
        private void TravelHistory(bool redo)
        {
            if(IsBusy) return;
            if(redo) edits.Redo(); else edits.Undo();
            LoadDraft(); ClearSettingsError(); UpdateEnabled();
        }
        internal async Task Connect()
        {
            await Execute("正在连接…",async delegate { var result=await Task.Run(()=>runtime.Control("connect")); if(!GuardianRuntime.Flag(result,"running")) throw new InvalidOperationException("代理未运行。"); ApplyState(await Task.Run(()=>runtime.State())); },true);
        }
        private async Task RefreshState(bool manual) { if(!IsConnected) { if(manual) await Connect(); return; } await Execute(manual?"正在刷新…":null,async delegate { ApplyState(await Task.Run(()=>runtime.State())); },true,manual,manual?BusyFeedback.Normal:BusyFeedback.BackgroundRefresh); }
        private async Task ChangeMode(string mode)
        {
            await Execute(null,async delegate { if(IsConnected) { await Task.Run(()=>runtime.SetMode(mode)); ApplyState(await Task.Run(()=>runtime.State())); } else { await Task.Run(()=>runtime.SetModeOffline(mode)); status.Text="启动模式已保存"; status.ForeColor=UI.Muted; ShowMode(mode); } },false,true,BusyFeedback.ModeSwitch);
        }
        private async Task SaveSettings()
        {
            var submitted=ReadDraft();
            if(!submitted.Valid) return;
            var settings=submitted.ToSettings();
            await Execute("正在保存…",async delegate {
                bool online=IsConnected;
                await Task.Run(()=>online?runtime.Save(settings):runtime.SaveOffline(settings));
                var saved=SettingsDraft.From(await Task.Run(()=>runtime.Preferences()));
                if(IsDisposed) return;
                edits.MarkSaved(saved,submitted); LoadDraft();
                if(online) ApplyState(await Task.Run(()=>runtime.State()));
                else { status.Text="启动设置已保存"; status.ForeColor=UI.Muted; }
            },false);
            settingsError=LastError.Length>0;
        }
        private async Task StopProxy()
        {
            await Execute("正在停止…",async delegate { var result=await Task.Run(()=>runtime.Control("stop")); if(GuardianRuntime.Flag(result,"running")) throw new InvalidOperationException("代理仍在运行。"); IsConnected=false; current=null; status.Text="代理已停止"; status.ForeColor=UI.Muted; summary.Text="Codex 路由已恢复到 CCSwitch"; counts.Text="本次运行    —"; signal.Listen=signal.Upstream="—"; signal.Health="未连接"; },false);
        }
        private async Task Execute(string progress,Func<Task> action,bool disconnectOnError,bool clearError=true,BusyFeedback feedback=BusyFeedback.Normal)
        {
            if(IsBusy) return; IsBusy=true; busyFeedback=feedback; if(progress!=null) status.Text=progress; UpdateEnabled();
            try { await action(); if(!IsDisposed&&clearError) { errorBox.Clear(); errorCard.Visible=false; } }
            catch(Exception error) { if(!IsDisposed) { if(disconnectOnError) { IsConnected=false; current=null; } errorBox.Text=error.Message; errorCard.Visible=true; status.Text=IsConnected?"操作未完成，代理仍在运行":"连接失败"; status.ForeColor=Color.FromArgb(146,65,35); } }
            finally { IsBusy=false; busyFeedback=BusyFeedback.Normal; if(!IsDisposed) UpdateEnabled(); }
        }
        private void ApplyState(Dictionary<string,object> state)
        {
            if(IsDisposed) return; current=state; IsConnected=true; ShowMode(GuardianRuntime.Text(state,"mode"));
            status.Text="v"+GuardianRuntime.Text(state,"version")+"  /  PID "+GuardianRuntime.Text(state,"pid"); status.ForeColor=UI.Muted;
            var active=GuardianRuntime.Map(state,"active");
            string nextListen=GuardianRuntime.Text(active,"listen"),nextUpstream=GuardianRuntime.Text(active,"upstream"),nextHealth=GuardianRuntime.Text(state,"upstream_status")=="可连接"?"上游已连接":"上游不可达";
            bool routeChanged=signal.Listen!=nextListen||signal.Upstream!=nextUpstream||signal.Health!=nextHealth||!signal.Running;
            signal.Listen=nextListen; signal.Upstream=nextUpstream; signal.Health=nextHealth; signal.Running=true; if(routeChanged) signal.Invalidate();
            summary.Text="提供商  "+GuardianRuntime.Text(active,"provider")+"  ·  "+signal.Health; var stats=GuardianRuntime.Map(state,"stats"); counts.Text="本次运行     转发 "+GuardianRuntime.Text(stats,"forwarded")+"     清理 "+GuardianRuntime.Text(stats,"proactive")+"     恢复 "+GuardianRuntime.Text(stats,"recovered")+"     失败 "+GuardianRuntime.Text(stats,"failed");
            restartRequired=GuardianRuntime.Flag(state,"guardian_restart_required"); pending.Visible=restartRequired; UpdateContentSizes();
            edits.RefreshSaved(SettingsDraft.From(GuardianRuntime.Map(state,"saved"))); LoadDraft(); UpdateEnabled();
        }
        private void ShowMode(string mode)
        {
            foreach(var item in modes) { bool selected=item.Key==mode; if(item.Value.Selected!=selected) { item.Value.Selected=selected; item.Value.Invalidate(); } }
            hint.Text=mode=="always"?"每次清理历史密文，会损失加密推理与压缩上下文。":mode=="on-error"?"先完整转发，遇到历史密文错误时清理并重试一次。":"完整保留请求与历史，直接返回上游结果。";
            hint.ForeColor=mode=="always"?UI.Warning:UI.Muted; hint.BackColor=mode=="always"?Color.FromArgb(255,248,228):UI.Tint;
        }
        private void UpdateEnabled()
        {
            bool valid=edits.Current==null||edits.Current.Valid;
            bool quiet=busyFeedback==BusyFeedback.ModeSwitch||busyFeedback==BusyFeedback.BackgroundRefresh;
            connect.SetActionState(!IsConnected,IsBusy,quiet); connect.Text=IsConnected?"已连接":"连接代理";
            refresh.SetActionState(true,IsBusy,quiet); stop.SetActionState(IsConnected,IsBusy,quiet);
            save.SetActionState(dirty&&valid,IsBusy,quiet); save.Text=IsConnected?"保存":"保存启动设置";
            undoSettings.SetActionState(edits.CanUndo,IsBusy,quiet); redoSettings.SetActionState(edits.CanRedo,IsBusy,quiet);
            modeNotice.Text=IsBusy&&busyFeedback==BusyFeedback.ModeSwitch?"正在切换…":"切换后立即生效";
            formState.Text=!valid?"重试次数须为 0–100 的整数":dirty?"尚未保存":"设置已保存"; formState.ForeColor=!valid?UI.Warning:dirty?UI.Accent:UI.Muted;
            foreach(var item in modes) item.Value.SetActionState(!IsConnected||(current!=null&&GuardianRuntime.Flag(current,"mode_switch_supported")),IsBusy,busyFeedback==BusyFeedback.BackgroundRefresh);
            if(pill.Running!=IsConnected) { pill.Running=IsConnected; pill.Invalidate(); }
            if(signal.Running!=IsConnected) { signal.Running=IsConnected; signal.Invalidate(); }
            navSettings.Text=dirty?"连接设置 *":"连接设置";
            bool hasError=errorBox.Text.Length>0;
            if(companion.Running!=IsConnected||companion.HasError!=hasError) { companion.Running=IsConnected; companion.HasError=hasError; companion.Invalidate(); }
        }
        private void FillSettings(Dictionary<string,object> settings)
        {
            edits.Reset(SettingsDraft.From(settings)); LoadDraft(); UpdateEnabled();
        }
        private void LoadDraft()
        {
            var draft=edits.Current; if(draft==null) return;
            filling=true;
            try { listen.Text=draft.Listen; upstream.Text=draft.Upstream; provider.Text=draft.Provider; config.Text=draft.Config; requestRetries.Text=draft.Requests; streamRetries.Text=draft.Streams; }
            finally { filling=false; }
        }
    }
}
