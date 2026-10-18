using System;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Reflection;
using System.Runtime.CompilerServices;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
using System.Windows.Forms;

[assembly: AssemblyTitle("Responses Guardian 管理器")]
[assembly: AssemblyProduct("Responses Guardian")]
[assembly: AssemblyVersion("1.6.3.0")]
[assembly: AssemblyFileVersion("1.6.3.0")]
[assembly: AssemblyCopyright("Copyright © 2026 Responses Guardian contributors")]
[assembly: InternalsVisibleTo("GuardianApp.Tests")]

namespace ResponsesGuardian.Desktop
{
    internal static class Program
    {
        [DllImport("user32.dll")] private static extern bool SetForegroundWindow(IntPtr window);
        [DllImport("user32.dll")] private static extern bool ShowWindow(IntPtr window, int command);
        [DllImport("user32.dll")] private static extern bool IsIconic(IntPtr window);

        [STAThread]
        private static void Main(string[] args)
        {
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);
            Application.SetUnhandledExceptionMode(UnhandledExceptionMode.CatchException);
            Application.ThreadException += delegate(object sender, ThreadExceptionEventArgs e) { MessageBox.Show(e.Exception.Message, "Guardian 管理器", MessageBoxButtons.OK, MessageBoxIcon.Error); };
            string path = Assembly.GetExecutingAssembly().Location;
            bool publicPreview=Array.IndexOf(args,"--public-preview")>=0;
            string root = Path.GetDirectoryName(path);
            string key;
            using (var hash = SHA256.Create()) key = BitConverter.ToString(hash.ComputeHash(Encoding.UTF8.GetBytes(path.ToUpperInvariant()))).Replace("-", "");
            bool created;
            using (var mutex = new Mutex(true, "Local\\ResponsesGuardianDesktop-" + key+(publicPreview?"-public":""), out created))
            {
                if (!created)
                {
                    FocusExisting(path,publicPreview);
                    return;
                }
                try { Application.Run(new GuardianForm(root,!publicPreview) { Text=publicPreview?"Guardian · 公开预览":"Guardian" }); }
                catch (Exception error) { MessageBox.Show(error.Message, "Guardian 管理器无法启动", MessageBoxButtons.OK, MessageBoxIcon.Error); }
                finally { mutex.ReleaseMutex(); }
            }
        }

        private static void FocusExisting(string path,bool publicPreview)
        {
            for (int attempt = 0; attempt < 20; attempt++)
            {
                foreach (var process in Process.GetProcessesByName(Path.GetFileNameWithoutExtension(path)))
                {
                    using (process)
                    {
                        try
                        {
                            if (process.Id != Process.GetCurrentProcess().Id &&
                                string.Equals(process.MainModule.FileName, path, StringComparison.OrdinalIgnoreCase) && process.MainWindowHandle != IntPtr.Zero &&
                                process.MainWindowTitle==(publicPreview?"Guardian · 公开预览":"Guardian"))
                            {
                                if(IsIconic(process.MainWindowHandle)) ShowWindow(process.MainWindowHandle, 9);
                                SetForegroundWindow(process.MainWindowHandle);
                                return;
                            }
                        }
                        catch (InvalidOperationException) { }
                        catch (System.ComponentModel.Win32Exception) { }
                    }
                }
                Thread.Sleep(100);
            }
        }
    }
}
