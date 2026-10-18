using System;
using System.Drawing;
using System.Drawing.Drawing2D;
using System.Runtime.InteropServices;
using System.Windows.Forms;

namespace ResponsesGuardian.Desktop
{
    internal static class UI
    {
        [DllImport("user32.dll")] private static extern uint GetDpiForWindow(IntPtr window);
        internal static int Dpi(Control control)
        {
            // Framework compatibility mode can report DeviceDpi=96 on a 120-DPI window.
            if(control.IsHandleCreated) {
                try { uint value=GetDpiForWindow(control.Handle); if(value>0) return (int)value; }
                catch(EntryPointNotFoundException) { }
            }
            var container=control as ContainerControl??control.FindForm();
            return container==null?96:(int)Math.Round(container.CurrentAutoScaleDimensions.Width);
        }
        internal static readonly Color Background = Color.FromArgb(245, 251, 255);
        internal static readonly Color Ink = Color.FromArgb(35, 62, 88);
        internal static readonly Color Muted = Color.FromArgb(101, 125, 145);
        internal static readonly Color Line = Color.FromArgb(214, 231, 241);
        internal static readonly Color Accent = Color.FromArgb(0, 150, 218);
        internal static readonly Color Tint = Color.FromArgb(231, 247, 255);
        internal static readonly Color Warning = Color.FromArgb(153, 99, 35);
        internal static Font Font(float size, FontStyle style = FontStyle.Regular) { return new Font("Microsoft YaHei UI", size, style); }
        internal static Color ParentColor(Control control)
        {
            for (Control parent = control.Parent; parent != null; parent = parent.Parent)
            {
                var surface = parent as Surface; if (surface != null) return surface.Fill;
                if (parent.BackColor.A == 255) return parent.BackColor;
            }
            return Background;
        }
        internal static GraphicsPath Round(RectangleF r, float radius)
        {
            float d = Math.Min(radius * 2, Math.Min(r.Width, r.Height));
            var p = new GraphicsPath();
            p.AddArc(r.X,r.Y,d,d,180,90); p.AddArc(r.Right-d,r.Y,d,d,270,90);
            p.AddArc(r.Right-d,r.Bottom-d,d,d,0,90); p.AddArc(r.X,r.Bottom-d,d,d,90,90);
            p.CloseFigure(); return p;
        }
        internal static GraphicsPath Cut(RectangleF r, float cut)
        {
            var p = new GraphicsPath();
            p.AddPolygon(new[] { new PointF(r.Left+cut,r.Top), new PointF(r.Right,r.Top), new PointF(r.Right-cut,r.Bottom), new PointF(r.Left,r.Bottom) }); return p;
        }
    }

    internal sealed class SmoothTable : TableLayoutPanel
    {
        internal SmoothTable() { DoubleBuffered=true; }
    }

    internal class Surface : Panel
    {
        internal Color Fill = Color.White;
        internal Color Border = Color.Transparent;
        internal int Radius = 6;
        internal Surface() { SetStyle(ControlStyles.UserPaint|ControlStyles.AllPaintingInWmPaint|ControlStyles.OptimizedDoubleBuffer|ControlStyles.ResizeRedraw,true); BackColor=Color.White; }
        protected override void OnPaintBackground(PaintEventArgs e) { e.Graphics.Clear(UI.ParentColor(this)); }
        protected override void OnPaint(PaintEventArgs e)
        {
            e.Graphics.SmoothingMode=SmoothingMode.AntiAlias;
            using(var p=UI.Round(new RectangleF(.5F,.5F,Width-1F,Height-1F),Radius*e.Graphics.DpiX/96F))
            {
                using(var b=new SolidBrush(Fill)) e.Graphics.FillPath(b,p);
                if(Border!=Color.Transparent) using(var pen=new Pen(Border)) e.Graphics.DrawPath(pen,p);
            }
            base.OnPaint(e);
        }
    }

    internal class ActionButton : Button
    {
        private bool retainEnabledAppearance;
        protected bool AppearanceEnabled { get { return Enabled||retainEnabledAppearance; } }
        internal void SetActionState(bool available,bool busy,bool retainAppearance)
        {
            bool before=AppearanceEnabled;
            // Keep native disabled semantics (mouse, keyboard, accessibility) during the lock.
            retainEnabledAppearance=available&&busy&&retainAppearance;
            Enabled=available&&!busy;
            if(before!=AppearanceEnabled) Invalidate();
        }
        protected override void OnEnabledChanged(EventArgs e) { Invalidate(); base.OnEnabledChanged(e); }
    }

    internal class ModernButton : ActionButton
    {
        internal bool Primary, Danger, Navigation, Chrome, Checked, Stepper;
        private bool hover, pressed;
        internal ModernButton()
        {
            SetStyle(ControlStyles.UserPaint|ControlStyles.AllPaintingInWmPaint|ControlStyles.OptimizedDoubleBuffer|ControlStyles.ResizeRedraw,true);
            FlatStyle=FlatStyle.Flat; FlatAppearance.BorderSize=0; Cursor=Cursors.Hand; Font=UI.Font(9.5F); Height=38;
        }
        protected override void OnMouseEnter(EventArgs e) { hover=true; Invalidate(); base.OnMouseEnter(e); }
        protected override void OnMouseLeave(EventArgs e) { hover=pressed=false; Invalidate(); base.OnMouseLeave(e); }
        protected override void OnMouseDown(MouseEventArgs e) { pressed=true; Invalidate(); base.OnMouseDown(e); }
        protected override void OnMouseUp(MouseEventArgs e) { pressed=false; Invalidate(); base.OnMouseUp(e); }
        protected override void OnPaint(PaintEventArgs e)
        {
            var g=e.Graphics; float s=g.DpiX/96F; g.Clear(UI.ParentColor(this)); g.SmoothingMode=SmoothingMode.AntiAlias;
            Color fill=Primary?UI.Accent:Color.White, text=Primary?Color.White:UI.Ink, border=Primary?UI.Accent:UI.Line;
            if(Danger) text=Color.FromArgb(178,82,89);
            if(hover&&AppearanceEnabled) fill=Primary?Color.FromArgb(0,130,199):UI.Tint;
            if(pressed&&AppearanceEnabled) fill=Primary?Color.FromArgb(0,111,179):Color.FromArgb(212,238,253);
            if(Stepper) { fill=pressed?Color.FromArgb(198,232,252):hover?Color.FromArgb(214,241,255):UI.Tint; text=UI.Accent; border=fill; }
            if(Navigation) { fill=Checked?UI.Accent:hover?UI.Tint:UI.ParentColor(this); border=fill; text=Checked?Color.White:UI.Muted; }
            if(Chrome) { fill=hover?UI.Tint:UI.ParentColor(this); border=fill; text=UI.Muted; if(Danger&&hover) { fill=Color.FromArgb(198,70,83); border=fill; text=Color.White; } }
            if(!AppearanceEnabled) { fill=Color.FromArgb(237,244,248); border=fill; text=Color.FromArgb(148,166,181); }
            using(var p=Navigation?UI.Cut(new RectangleF(.5F,.5F,Width-1F,Height-1F),10*s):UI.Round(new RectangleF(.5F,.5F,Width-1F,Height-1F),(Chrome?1:5)*s))
            { using(var b=new SolidBrush(fill)) g.FillPath(b,p); using(var pen=new Pen(border)) g.DrawPath(pen,p); }
            var textFlags=TextFormatFlags.VerticalCenter|TextFormatFlags.HorizontalCenter|TextFormatFlags.NoPrefix;
            textFlags|=Stepper?TextFormatFlags.NoPadding:TextFormatFlags.EndEllipsis;
            TextRenderer.DrawText(g,Text,Font,ClientRectangle,text,textFlags);
            if(Focused&&ShowFocusCues) using(var pen=new Pen(Primary?Color.White:UI.Accent) { DashStyle=DashStyle.Dot }) g.DrawRectangle(pen,5,5,Width-11,Height-11);
        }
    }

    internal sealed class ModeButton : ActionButton
    {
        private readonly Font titleFont=UI.Font(10.4F,FontStyle.Bold);
        private readonly Font codeFont=new Font("Consolas",8.7F);
        internal string Title, Code;
        internal bool Selected;
        private bool hover;
        internal ModeButton() { SetStyle(ControlStyles.UserPaint|ControlStyles.AllPaintingInWmPaint|ControlStyles.OptimizedDoubleBuffer|ControlStyles.ResizeRedraw,true); FlatStyle=FlatStyle.Flat; FlatAppearance.BorderSize=0; Cursor=Cursors.Hand; }
        protected override void OnMouseEnter(EventArgs e) { hover=true; Invalidate(); base.OnMouseEnter(e); }
        protected override void OnMouseLeave(EventArgs e) { hover=false; Invalidate(); base.OnMouseLeave(e); }
        protected override void Dispose(bool disposing) { if(disposing) { titleFont.Dispose(); codeFont.Dispose(); } base.Dispose(disposing); }
        protected override void OnPaint(PaintEventArgs e)
        {
            var g=e.Graphics; float s=g.DpiX/96F; g.Clear(UI.ParentColor(this)); g.SmoothingMode=SmoothingMode.AntiAlias;
            Color fill=Selected?UI.Accent:hover?UI.Tint:Color.FromArgb(241,248,253), ink=!AppearanceEnabled?UI.Muted:Selected?Color.White:UI.Ink;
            using(var p=UI.Cut(new RectangleF(1,1,Width-2,Height-3),10*s))
            { using(var b=new SolidBrush(fill)) g.FillPath(b,p); using(var pen=new Pen(Selected?UI.Accent:UI.Line)) g.DrawPath(pen,p); }
            TextRenderer.DrawText(g,Title,titleFont,new Rectangle((int)(12*s),(int)(Height*.20F),Width-(int)(20*s),(int)(24*s)),ink,TextFormatFlags.HorizontalCenter|TextFormatFlags.NoPrefix);
            TextRenderer.DrawText(g,Code,codeFont,new Rectangle(0,(int)(Height*.60F),Width,(int)(20*s)),Selected?Color.FromArgb(215,246,255):UI.Muted,TextFormatFlags.HorizontalCenter|TextFormatFlags.NoPrefix);
            if(Selected) using(var b=new SolidBrush(Color.FromArgb(255,217,99))) g.FillPolygon(b,new[]{new PointF(Width-26*s,0),new PointF(Width-4*s,0),new PointF(Width-8*s,5*s),new PointF(Width-30*s,5*s)});
            if(Focused&&ShowFocusCues) using(var pen=new Pen(Selected?Color.White:UI.Accent) { DashStyle=DashStyle.Dot }) g.DrawRectangle(pen,12,7,Width-25,Height-15);
        }
    }

    internal sealed class StatusPill : Control
    {
        internal bool Running;
        internal StatusPill() { SetStyle(ControlStyles.UserPaint|ControlStyles.AllPaintingInWmPaint|ControlStyles.OptimizedDoubleBuffer|ControlStyles.ResizeRedraw,true); Font=UI.Font(8.7F); }
        protected override void OnPaint(PaintEventArgs e)
        {
            float s=e.Graphics.DpiX/96F; e.Graphics.SmoothingMode=SmoothingMode.AntiAlias; e.Graphics.Clear(UI.ParentColor(this));
            Color ink=Running?Color.FromArgb(17,132,150):UI.Muted;
            using(var b=new SolidBrush(ink)) e.Graphics.FillEllipse(b,10*s,Height/2F-3*s,6*s,6*s);
            TextRenderer.DrawText(e.Graphics,Running?"代理运行中":"尚未连接",Font,new Rectangle((int)(24*s),0,Width-(int)(25*s),Height),ink,TextFormatFlags.VerticalCenter|TextFormatFlags.NoPrefix);
        }
    }

    internal sealed class InputSurface : Surface
    {
        internal InputSurface(Control editor)
        {
            Radius=4; Border=UI.Line; Height=36; Padding=new Padding(10,7,8,5);
            editor.Dock=DockStyle.Fill; editor.Margin=new Padding(0); editor.BackColor=Color.White; editor.ForeColor=UI.Ink;
            var text=editor as TextBox; if(text!=null) { text.BorderStyle=BorderStyle.None; text.Font=new Font("Consolas",10F); }
            var number=editor as NumericUpDown; if(number!=null) { number.BorderStyle=BorderStyle.None; number.Font=UI.Font(10F); }
            Controls.Add(editor); editor.GotFocus+=delegate { Border=UI.Accent; Invalidate(); }; editor.LostFocus+=delegate { Border=UI.Line; Invalidate(); };
        }
    }

    internal sealed class RetryInput : Surface
    {
        private readonly TextBox editor=new TextBox();
        private readonly ModernButton decrease=new ModernButton();
        private readonly ModernButton increase=new ModernButton();
        private int lastValid;
        internal event EventHandler ValueChanged;
        internal RetryInput()
        {
            Radius=5; Border=UI.Line; Height=38; Font=UI.Font(10F);
            editor.BorderStyle=BorderStyle.None; editor.TextAlign=HorizontalAlignment.Center;
            editor.Font=UI.Font(10F); editor.ForeColor=UI.Ink; editor.Text="0"; editor.AccessibleName="重试次数";
            decrease.Text="−"; increase.Text="+"; decrease.Stepper=increase.Stepper=true;
            decrease.Font=increase.Font=new Font("Segoe UI",13F);
            decrease.AccessibleName="减少重试次数"; increase.AccessibleName="增加重试次数";
            decrease.Click+=delegate { Step(-1); }; increase.Click+=delegate { Step(1); };
            editor.KeyDown+=delegate(object sender,KeyEventArgs e) {
                if(e.KeyCode==Keys.Up||e.KeyCode==Keys.Down) { Step(e.KeyCode==Keys.Up?1:-1); e.Handled=true; e.SuppressKeyPress=true; }
            };
            editor.TextChanged+=delegate {
                int value; if(TryGetValue(out value)) lastValid=value;
                UpdateAppearance();
                if(ValueChanged!=null) ValueChanged(this,EventArgs.Empty);
            };
            editor.GotFocus+=delegate { UpdateAppearance(); };
            editor.LostFocus+=delegate { UpdateAppearance(); };
            Controls.Add(editor); Controls.Add(decrease); Controls.Add(increase); UpdateAppearance();
        }
        internal int Value { get { int value; return TryGetValue(out value)?value:lastValid; } set { Text=Math.Max(0,Math.Min(100,value)).ToString(System.Globalization.CultureInfo.InvariantCulture); } }
        public override string Text { get { return editor==null?base.Text:editor.Text; } set { if(editor==null) base.Text=value; else editor.Text=value; } }
        internal bool TryGetValue(out int value) { return SettingsDraft.ParseRetries(Text,out value); }
        private void Step(int direction) { Value=Value+direction; editor.Focus(); editor.SelectAll(); }
        private void UpdateAppearance()
        {
            int value; bool valid=TryGetValue(out value);
            Border=!valid?UI.Warning:editor.Focused?UI.Accent:UI.Line;
            decrease.Enabled=!valid||value>0; increase.Enabled=!valid||value<100; Invalidate();
        }
        protected override void OnLayout(LayoutEventArgs e)
        {
            base.OnLayout(e); if(editor==null) return;
            float s=UI.Dpi(this)/96F; int pad=(int)(4*s),button=(int)Math.Min(28*s,Math.Max(22*s,(Width-64*s)/2)),gap=(int)(4*s);
            decrease.SetBounds(pad,pad,button,Math.Max(1,Height-pad*2));
            increase.SetBounds(Width-pad-button,pad,button,Math.Max(1,Height-pad*2));
            editor.SetBounds(pad+button+gap,Math.Max(0,(Height-editor.PreferredHeight)/2),Math.Max(1,Width-2*(pad+button+gap)),editor.PreferredHeight);
            decrease.Name=Name+"-Decrease"; increase.Name=Name+"-Increase"; editor.Name=Name+"-Editor";
        }
    }

    internal sealed class SignalDisplay : Control
    {
        internal const int MinimumLogicalHeight=120;
        internal bool Running;
        internal string Listen="—", Upstream="—", Health="尚未连接";
        private Font nameFont,addressFont,clientFont;
        private float fontDpi;
        internal SignalDisplay() { SetStyle(ControlStyles.UserPaint|ControlStyles.AllPaintingInWmPaint|ControlStyles.OptimizedDoubleBuffer|ControlStyles.ResizeRedraw,true); }
        internal static Rectangle LabelBounds(Size size,float scale,int node,bool detail)
        {
            float column=size.Width/3F;
            return Rectangle.FromLTRB((int)Math.Ceiling(column*node+6*scale),(int)(scale*(detail?93:66)),
                (int)Math.Floor(column*(node+1)-6*scale),(int)Math.Ceiling(scale*(detail?115:90)));
        }
        protected override void Dispose(bool disposing)
        {
            if(disposing) DisposeFonts();
            base.Dispose(disposing);
        }
        private void DisposeFonts()
        {
            if(nameFont!=null) nameFont.Dispose();
            if(addressFont!=null) addressFont.Dispose();
            if(clientFont!=null) clientFont.Dispose();
        }
        private void EnsureFonts(float dpi)
        {
            if(nameFont!=null&&fontDpi==dpi) return;
            DisposeFonts(); fontDpi=dpi;
            nameFont=new Font("Segoe UI",9.5F*dpi/72F,FontStyle.Bold,GraphicsUnit.Pixel);
            addressFont=new Font("Consolas",8.5F*dpi/72F,FontStyle.Regular,GraphicsUnit.Pixel);
            clientFont=new Font("Microsoft YaHei UI",8.5F*dpi/72F,FontStyle.Regular,GraphicsUnit.Pixel);
        }
        protected override void OnPaint(PaintEventArgs e)
        {
            var g=e.Graphics; float s=g.DpiX/96F; g.SmoothingMode=SmoothingMode.AntiAlias; g.Clear(UI.ParentColor(this));
            EnsureFonts(g.DpiY);
            using(var p=UI.Round(new RectangleF(1,1,Width-2,Height-2),6*s))
            { using(var b=new SolidBrush(Color.FromArgb(242,250,255))) g.FillPath(b,p); using(var pen=new Pen(UI.Line)) g.DrawPath(pen,p); }
            float left=Width/6F, middle=Width*.5F, right=Width*5F/6F, y=40*s;
            using(var wire=new Pen(Running?Color.FromArgb(125,205,239):UI.Line,1.5F*s))
            {
                g.DrawLine(wire,left+24*s,y,middle-24*s,y); g.DrawLine(wire,middle+24*s,y,right-24*s,y);
                g.DrawLines(wire,new[]{new PointF(middle-31*s,y-4*s),new PointF(middle-25*s,y),new PointF(middle-31*s,y+4*s)});
                g.DrawLines(wire,new[]{new PointF(right-31*s,y-4*s),new PointF(right-25*s,y),new PointF(right-31*s,y+4*s)});
            }
            float[] xs={left,middle,right}; string[] names={"Codex","Guardian","CCSwitch"};
            for(int i=0;i<3;i++)
            {
                float x=xs[i]; using(var b=new SolidBrush(i==1?UI.Accent:Color.White)) g.FillEllipse(b,x-18*s,y-18*s,36*s,36*s);
                using(var pen=new Pen(i==1?UI.Accent:UI.Line,1.2F*s)) g.DrawEllipse(pen,x-18*s,y-18*s,36*s,36*s);
                using(var pen=new Pen(i==1?Color.White:UI.Accent,1.7F*s))
                {
                    if(i==0) { g.DrawLines(pen,new[]{new PointF(x-4*s,y-5*s),new PointF(x-9*s,y),new PointF(x-4*s,y+5*s)}); g.DrawLines(pen,new[]{new PointF(x+4*s,y-5*s),new PointF(x+9*s,y),new PointF(x+4*s,y+5*s)}); }
                    else if(i==1) g.DrawPolygon(pen,new[]{new PointF(x,y-9*s),new PointF(x+8*s,y-5*s),new PointF(x+6*s,y+4*s),new PointF(x,y+9*s),new PointF(x-6*s,y+4*s),new PointF(x-8*s,y-5*s)});
                    else { g.DrawLine(pen,x-7*s,y-4*s,x+7*s,y-4*s); g.DrawLine(pen,x-7*s,y+4*s,x+7*s,y+4*s); g.DrawLine(pen,x+3*s,y-8*s,x+7*s,y-4*s); g.DrawLine(pen,x-3*s,y+8*s,x-7*s,y+4*s); }
                }
                TextRenderer.DrawText(g,names[i],nameFont,LabelBounds(ClientSize,s,i,false),UI.Ink,TextFormatFlags.HorizontalCenter|TextFormatFlags.NoPrefix|TextFormatFlags.EndEllipsis);
            }
            var flags=TextFormatFlags.HorizontalCenter|TextFormatFlags.EndEllipsis|TextFormatFlags.NoPrefix;
            TextRenderer.DrawText(g,Listen,addressFont,LabelBounds(ClientSize,s,1,true),UI.Muted,flags);
            TextRenderer.DrawText(g,Upstream.Replace("http://",""),addressFont,LabelBounds(ClientSize,s,2,true),UI.Muted,flags);
            TextRenderer.DrawText(g,Running?"本机客户端":"等待连接",clientFont,LabelBounds(ClientSize,s,0,true),UI.Muted,flags);
        }
    }

    internal struct CompanionGeometry
    {
        internal RectangleF Artwork,Orbit,Bubble;
    }

    internal sealed class CompanionScene : Control
    {
        internal bool Running, HasError;
        private readonly Image portrait;
        private Bitmap quickPortrait,displayPortrait;
        private bool interactiveResize;
        private readonly Font greetingFont=UI.Font(22F,FontStyle.Bold);
        private readonly Font subtitleFont=new Font("Segoe UI",8F,FontStyle.Bold);
        private readonly Font messageFont=UI.Font(9F);
        private readonly Font captionFont=UI.Font(7.5F);
        internal bool HasPersonalSkin { get { return portrait!=null; } }
        private int lineIndex;
        internal CompanionGeometry Geometry { get { return MeasureGeometry(ClientSize,UI.Dpi(this)/96F,portrait==null?220F/235F:(float)portrait.Width/portrait.Height); } }
        internal static CompanionGeometry MeasureGeometry(Size size,float s,float aspect)
        {
            float availableWidth=Math.Max(1,size.Width-32*s),availableHeight=Math.Max(1,size.Height-244*s);
            float h=Math.Min(availableHeight,availableWidth/aspect),w=h*aspect;
            var artwork=new RectangleF((size.Width-w)/2,110*s+(availableHeight-h)/2,w,h);
            var bubble=new RectangleF(20*s,size.Height-115*s,size.Width-40*s,90*s);
            // The circle belongs to the background, independent of the portrait's size or aspect.
            float top=106*s,bottom=bubble.Top-12*s;
            float diameter=Math.Max(1,Math.Min(Math.Min(size.Width*.80F,size.Height*.44F),Math.Min(availableWidth,bottom-top)));
            float centerY=Math.Max(top+diameter/2,Math.Min(size.Height*.40F,bottom-diameter/2));
            return new CompanionGeometry {
                Artwork=artwork,
                Orbit=new RectangleF(size.Width/2F-diameter/2,centerY-diameter/2,diameter,diameter),
                Bubble=bubble
            };
        }
        internal CompanionScene(bool usePersonalSkin)
        {
            SetStyle(ControlStyles.UserPaint|ControlStyles.AllPaintingInWmPaint|ControlStyles.OptimizedDoubleBuffer|ControlStyles.ResizeRedraw|ControlStyles.Selectable,true);
            Cursor=Cursors.Hand; TabStop=true; AccessibleRole=AccessibleRole.PushButton; AccessibleName="小助手，点击或按空格查看提示";
            portrait=PersonalSkin.Load(usePersonalSkin);
            if(portrait!=null) {
                float factor=Math.Min(1,512F/Math.Max(portrait.Width,portrait.Height));
                quickPortrait=ResamplePortrait(new Size(Math.Max(1,(int)(portrait.Width*factor)),Math.Max(1,(int)(portrait.Height*factor))));
            }
        }
        internal void SetInteractiveResize(bool value) { if(interactiveResize!=value) { interactiveResize=value; Invalidate(); } }
        private Bitmap ResamplePortrait(Size size)
        {
            var bitmap=new Bitmap(size.Width,size.Height,System.Drawing.Imaging.PixelFormat.Format32bppPArgb);
            using(var g=Graphics.FromImage(bitmap)) {
                g.CompositingMode=CompositingMode.SourceCopy; g.InterpolationMode=InterpolationMode.HighQualityBicubic;
                g.PixelOffsetMode=PixelOffsetMode.HighQuality;
                g.DrawImage(portrait,new Rectangle(Point.Empty,size));
            }
            return bitmap;
        }
        protected override void Dispose(bool disposing)
        {
            if(disposing) {
                if(portrait!=null) portrait.Dispose(); if(quickPortrait!=null) quickPortrait.Dispose(); if(displayPortrait!=null) displayPortrait.Dispose();
                greetingFont.Dispose(); subtitleFont.Dispose(); messageFont.Dispose(); captionFont.Dispose();
            }
            base.Dispose(disposing);
        }
        protected override void OnClick(EventArgs e) { lineIndex=(lineIndex+1)%4; Invalidate(); base.OnClick(e); }
        protected override void OnKeyDown(KeyEventArgs e) { if(e.KeyCode==Keys.Space||e.KeyCode==Keys.Enter) { OnClick(EventArgs.Empty); e.Handled=true; e.SuppressKeyPress=true; } base.OnKeyDown(e); }
        protected override void OnGotFocus(EventArgs e) { Invalidate(); base.OnGotFocus(e); }
        protected override void OnLostFocus(EventArgs e) { Invalidate(); base.OnLostFocus(e); }
        protected override void OnPaint(PaintEventArgs e)
        {
            var g=e.Graphics; float s=g.DpiX/96F; g.SmoothingMode=SmoothingMode.AntiAlias; g.InterpolationMode=InterpolationMode.HighQualityBicubic;
            var geometry=MeasureGeometry(ClientSize,s,portrait==null?220F/235F:(float)portrait.Width/portrait.Height);
            using(var b=new LinearGradientBrush(ClientRectangle,Color.FromArgb(222,245,255),Color.FromArgb(247,252,255),65F)) g.FillRectangle(b,ClientRectangle);
            using(var b=new SolidBrush(Color.FromArgb(201,236,254))) g.FillPolygon(b,new[]{new PointF(0,Height*.44F),new PointF(Width,Height*.23F),new PointF(Width,Height*.42F),new PointF(0,Height*.63F)});
            using(var pen=new Pen(Color.FromArgb(172,222,247),1*s))
            {
                g.DrawEllipse(pen,geometry.Orbit);
                g.DrawLine(pen,0,Height*.77F,Width,Height*.56F); g.DrawLine(pen,0,Height*.79F,Width,Height*.58F);
            }
            using(var b=new SolidBrush(Color.FromArgb(186,222,239)))
                for(float x=18*s;x<Width;x+=18*s) for(float y=Height*.69F;y<Height;y+=18*s) g.FillEllipse(b,x,y,1.7F*s,1.7F*s);
            string greeting=DateTime.Now.Hour<11?"早上好。":DateTime.Now.Hour<18?"下午好。":"晚上好。";
            TextRenderer.DrawText(g,greeting,greetingFont,new Rectangle((int)(28*s),(int)(27*s),Width-(int)(40*s),(int)(47*s)),UI.Ink,TextFormatFlags.NoPrefix);
            TextRenderer.DrawText(g,"LET’S STAY CONNECTED",subtitleFont,new Rectangle((int)(31*s),(int)(77*s),Width-(int)(40*s),(int)(25*s)),Color.FromArgb(52,143,185),TextFormatFlags.NoPrefix);
            using(var pen=new Pen(Color.FromArgb(255,204,84),2*s)) { float x=Width-36*s,y=47*s; g.DrawLine(pen,x-7*s,y,x+7*s,y); g.DrawLine(pen,x,y-7*s,x,y+7*s); }
            var artwork=geometry.Artwork;
            using(var b=new SolidBrush(Color.FromArgb(55,114,182,218))) g.FillEllipse(b,artwork.Left+artwork.Width*.2F,artwork.Bottom-2*s,artwork.Width*.6F,10*s);
            if(portrait!=null) {
                if(interactiveResize) {
                    g.InterpolationMode=InterpolationMode.Bilinear; g.DrawImage(quickPortrait,artwork);
                } else {
                    var size=new Size(Math.Max(1,(int)Math.Round(artwork.Width)),Math.Max(1,(int)Math.Round(artwork.Height)));
                    if(displayPortrait==null||displayPortrait.Size!=size) {
                        if(displayPortrait!=null) displayPortrait.Dispose(); displayPortrait=ResamplePortrait(size);
                    }
                    g.DrawImageUnscaled(displayPortrait,(int)Math.Round(artwork.X),(int)Math.Round(artwork.Y));
                }
            }
            else DrawDefaultCompanion(g,artwork);
            var bubble=geometry.Bubble;
            using(var p=UI.Round(bubble,8*s)) { using(var b=new SolidBrush(Color.FromArgb(244,255,255,255))) g.FillPath(b,p); using(var pen=new Pen(Color.FromArgb(187,223,242))) g.DrawPath(pen,p); }
            string line;
            if(HasError) line="这一步没完成。\n右边保留了原因，一起看看吧。";
            else if(!Running) line="我在这里。\n先连接代理，再继续对话吧。";
            else if(lineIndex==1) line="不用一直守着窗口。\n关掉它，后台代理仍会继续运行。";
            else if(lineIndex==2) line="切换模式不用重启。\n下一个请求就会按新模式处理。";
            else if(lineIndex==3) line="「出错后恢复」先完整转发。\n密文出错时，再清理重试。";
            else line="代理已经运行。\n准备好了，继续今天的对话吧。";
            TextRenderer.DrawText(g,line,messageFont,new Rectangle((int)(33*s),Height-(int)(101*s),Width-(int)(65*s),(int)(48*s)),UI.Ink,TextFormatFlags.NoPrefix|TextFormatFlags.WordBreak);
            TextRenderer.DrawText(g,"· 点我一下",captionFont,new Rectangle((int)(33*s),Height-(int)(48*s),Width-(int)(64*s),(int)(18*s)),UI.Muted,TextFormatFlags.Right|TextFormatFlags.NoPrefix);
            if(Focused&&ShowFocusCues) using(var pen=new Pen(UI.Accent) { DashStyle=DashStyle.Dot }) g.DrawRectangle(pen,4,4,Width-9,Height-9);
        }
        private void DrawDefaultCompanion(Graphics g,RectangleF artwork)
        {
            float scale=artwork.Height/235F;
            var state=g.Save(); g.TranslateTransform(artwork.Left,artwork.Top); g.ScaleTransform(scale,scale);
            using(var white=new SolidBrush(Color.White))
            using(var tint=new SolidBrush(UI.Tint))
            using(var blue=new SolidBrush(UI.Accent))
            using(var ink=new SolidBrush(UI.Ink))
            using(var line=new Pen(Color.FromArgb(115,188,224),2)) {
                g.DrawLine(line,108,20,108,43);
                using(var dot=new SolidBrush(Color.FromArgb(255,206,87))) g.FillEllipse(dot,102,10,12,12);
                g.FillEllipse(white,8,92,34,45); g.DrawEllipse(line,8,92,34,45);
                g.FillEllipse(white,178,92,34,45); g.DrawEllipse(line,178,92,34,45);
                using(var body=UI.Round(new RectangleF(65,156,86,55),17)) { g.FillPath(white,body); g.DrawPath(line,body); }
                using(var head=UI.Round(new RectangleF(26,38,164,131),40)) { g.FillPath(white,head); g.DrawPath(line,head); }
                using(var face=UI.Round(new RectangleF(46,69,124,64),23)) g.FillPath(ink,face);
                using(var eye1=UI.Round(new RectangleF(72,87,13,24),6))
                using(var eye2=UI.Round(new RectangleF(131,87,13,24),6))
                using(var light=new SolidBrush(Color.FromArgb(118,225,255))) { g.FillPath(light,eye1); g.FillPath(light,eye2); }
                using(var smile=new Pen(Color.FromArgb(118,225,255),2)) g.DrawArc(smile,98,109,20,10,0,180);
                g.FillPolygon(blue,new[]{new PointF(108,176),new PointF(120,181),new PointF(117,193),new PointF(108,201),new PointF(99,193),new PointF(96,181)});
                using(var foot1=UI.Round(new RectangleF(63,207,30,13),6))
                using(var foot2=UI.Round(new RectangleF(126,207,30,13),6)) { g.FillPath(tint,foot1); g.FillPath(tint,foot2); g.DrawPath(line,foot1); g.DrawPath(line,foot2); }
            }
            g.Restore(state);
        }
    }
}
