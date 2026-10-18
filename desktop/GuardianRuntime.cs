using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Net;
using System.Text;
using System.Text.RegularExpressions;
using System.Web.Script.Serialization;

namespace ResponsesGuardian.Desktop
{
    internal sealed class GuardianRuntime
    {
        internal readonly string Root;
        private readonly JavaScriptSerializer json = new JavaScriptSerializer();
        private Uri panel;
        private string token;

        internal GuardianRuntime(string root)
        {
            Root = Path.GetFullPath(root);
        }

        internal Dictionary<string, object> Control(string action, string settingsFile = null, string mode = null)
        {
            string script = Path.Combine(Root, "scripts", "desktop-control.ps1");
            if (!File.Exists(script)) throw new InvalidOperationException("缺少 scripts\\desktop-control.ps1，请保留完整的应用目录。");
            string runtime = Path.Combine(Root, "runtime");
            Directory.CreateDirectory(runtime);
            string result = Path.Combine(runtime, "desktop-operation-" + Guid.NewGuid().ToString("N") + ".json");
            string shell = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows), "System32", "WindowsPowerShell", "v1.0", "powershell.exe");
            var start = new ProcessStartInfo(shell, "-NoLogo -NoProfile -ExecutionPolicy Bypass -File " + QuoteArgument(script) + " -Action " + action + " -ResultFile " + QuoteArgument(result));
            if (settingsFile != null) start.Arguments += " -SettingsFile " + QuoteArgument(settingsFile);
            if (mode != null) start.Arguments += " -Mode " + QuoteArgument(mode);
            start.WorkingDirectory = Root;
            start.UseShellExecute = false;
            start.CreateNoWindow = true;
            start.WindowStyle = ProcessWindowStyle.Hidden;
            try
            {
                using (var process = Process.Start(start))
                {
                    if (!process.WaitForExit(35000))
                    {
                        // Kill only our controller shell, never the separate proxy.
                        try { process.Kill(); } catch (InvalidOperationException) { }
                        throw new InvalidOperationException("操作超时。代理可能已启动，请点击“启动 / 连接”重新检查。");
                    }
                    if (!File.Exists(result)) throw new InvalidOperationException("未收到操作结果。请检查应用目录权限和 Windows PowerShell 是否可用。");
                    var data = Parse(File.ReadAllText(result, Encoding.UTF8));
                    if (!Flag(data, "ok")) throw new InvalidOperationException(Text(data, "error"));
                    if (process.ExitCode != 0) throw new InvalidOperationException("代理控制操作返回错误：" + process.ExitCode);
                    if (Flag(data, "running"))
                    {
                        Uri candidate;
                        if (!Uri.TryCreate(Text(data, "panel_url"), UriKind.Absolute, out candidate) ||
                            candidate.Scheme != "http" || !candidate.IsLoopback || candidate.UserInfo != "" ||
                            candidate.AbsolutePath != "/__responses_guardian/panel" || candidate.Query != "" || candidate.Fragment != "")
                            throw new InvalidOperationException("代理返回的面板地址不是有效的本机地址。");
                        panel = candidate;
                        token = null;
                    }
                    else { panel = null; token = null; }
                    return data;
                }
            }
            finally { TryDelete(result); TryDelete(result + ".tmp"); }
        }

        internal Dictionary<string, object> State() { return Request("state", "GET", null); }
        internal Dictionary<string, object> SetMode(string mode) { return Request("mode", "POST", new Dictionary<string, object> { { "mode", mode } }); }
        internal Dictionary<string, object> SetModeOffline(string mode) { return Control("mode", null, mode); }
        internal Dictionary<string, object> Save(Dictionary<string, object> settings) { return Request("settings", "PUT", settings); }

        internal Dictionary<string, object> SaveOffline(Dictionary<string, object> settings)
        {
            string directory = Path.Combine(Root, "runtime"); Directory.CreateDirectory(directory);
            string path = Path.Combine(directory, "desktop-input-" + Guid.NewGuid().ToString("N") + ".json");
            try { File.WriteAllText(path, json.Serialize(settings), new UTF8Encoding(false)); return Control("save", path); }
            finally { TryDelete(path); }
        }

        internal Dictionary<string, object> Preferences()
        {
            var settings = new Dictionary<string, object> {
                { "listen", "127.0.0.1:15722" }, { "upstream", "http://127.0.0.1:15721" }, { "provider", "custom" },
                { "codex_config", Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".codex", "config.toml") },
                { "request_max_retries", 20 }, { "stream_max_retries", 20 }
            };
            try
            {
                string path = Path.Combine(Root, "runtime", "settings.json");
                if (!File.Exists(path)) return settings;
                var saved = Parse(File.ReadAllText(path, Encoding.UTF8));
                foreach (string key in new[] { "listen", "upstream", "provider", "codex_config" })
                    if (saved.ContainsKey(key) && saved[key] is string) settings[key] = saved[key];
                foreach (string key in new[] { "request_max_retries", "stream_max_retries" })
                    if (saved.ContainsKey(key) && saved[key] is int && Number(saved, key) >= 0 && Number(saved, key) <= 100) settings[key] = saved[key];
            }
            catch (IOException) { } catch (UnauthorizedAccessException) { } catch (InvalidOperationException) { }
            return settings;
        }

        private Dictionary<string, object> Request(string path, string method, object body)
        {
            if (panel == null) throw new InvalidOperationException("代理未连接，请先点击“启动 / 连接”。");
            if (body != null && token == null) LoadToken();
            var uri = new Uri(panel, path);
            for (int attempt = 0; attempt < 2; attempt++)
            {
                var request = NewRequest(uri);
                request.Method = method;
                if (body != null)
                {
                    byte[] bytes = Encoding.UTF8.GetBytes(json.Serialize(body));
                    request.ContentType = "application/json";
                    request.ContentLength = bytes.Length;
                    request.Headers["Origin"] = panel.GetLeftPart(UriPartial.Authority);
                    request.Headers["X-Guardian-Token"] = token;
                    using (var stream = request.GetRequestStream()) stream.Write(bytes, 0, bytes.Length);
                }
                try
                {
                    using (var response = (HttpWebResponse)request.GetResponse())
                    {
                        if (response.StatusCode != HttpStatusCode.OK) throw new InvalidOperationException("本地面板返回异常状态：" + (int)response.StatusCode);
                        return Parse(Read(response));
                    }
                }
                catch (WebException error)
                {
                    using (var response = error.Response as HttpWebResponse)
                    {
                        if (response != null)
                        {
                            if (body != null && response.StatusCode == HttpStatusCode.Forbidden && attempt == 0)
                            {
                                LoadToken();
                                continue;
                            }
                            string message = "本地面板返回错误：" + (int)response.StatusCode;
                            try { var data = Parse(Read(response)); if (Text(data, "error") != "") message = Text(data, "error"); } catch (InvalidOperationException) { }
                            throw new InvalidOperationException(message, error);
                        }
                        throw new InvalidOperationException("无法连接代理。请点击“启动 / 连接”检查其运行状态。", error);
                    }
                }
            }
            throw new InvalidOperationException("无法验证本地面板，请重新连接。");
        }

        private void LoadToken()
        {
            var request = NewRequest(panel);
            using (var response = (HttpWebResponse)request.GetResponse())
            {
                if (response.StatusCode != HttpStatusCode.OK) throw new InvalidOperationException("无法读取本地面板。");
                var match = Regex.Match(Read(response), @"token='([a-f0-9]{64})'");
                if (!match.Success) throw new InvalidOperationException("当前代理的面板格式不兼容，请检查代理版本。");
                token = match.Groups[1].Value;
            }
        }

        private static HttpWebRequest NewRequest(Uri uri)
        {
            var request = (HttpWebRequest)WebRequest.Create(uri);
            request.Proxy = null;
            request.AllowAutoRedirect = false;
            // The stop script checks live Codex connections. UI polling must
            // not leave its own idle connections looking like active Codex.
            request.KeepAlive = false;
            request.Timeout = 8000;
            request.ReadWriteTimeout = 8000;
            request.UserAgent = "ResponsesGuardian-Desktop/1.0";
            return request;
        }

        private static string Read(HttpWebResponse response)
        {
            using (var input = response.GetResponseStream())
            using (var output = new MemoryStream())
            {
                byte[] buffer = new byte[4096];
                int count;
                while ((count = input.Read(buffer, 0, buffer.Length)) > 0)
                {
                    if (output.Length + count > 256 * 1024) throw new InvalidOperationException("本地面板响应过大。");
                    output.Write(buffer, 0, count);
                }
                return Encoding.UTF8.GetString(output.ToArray());
            }
        }

        private Dictionary<string, object> Parse(string text)
        {
            try { var data = json.DeserializeObject(text) as Dictionary<string, object>; if (data != null) return data; }
            catch (ArgumentException) { }
            throw new InvalidOperationException("无法读取本地操作结果。");
        }

        internal static Dictionary<string, object> Map(Dictionary<string, object> data, string key)
        {
            object value;
            return data.TryGetValue(key, out value) && value is Dictionary<string, object> ? (Dictionary<string, object>)value : new Dictionary<string, object>();
        }
        internal static string Text(Dictionary<string, object> data, string key) { object value; return data.TryGetValue(key, out value) && value != null ? Convert.ToString(value) : ""; }
        internal static bool Flag(Dictionary<string, object> data, string key) { object value; return data.TryGetValue(key, out value) && value is bool && (bool)value; }
        internal static int Number(Dictionary<string, object> data, string key) { object value; return data.TryGetValue(key, out value) ? Convert.ToInt32(value) : 0; }
        private static void TryDelete(string path) { try { File.Delete(path); } catch (IOException) { } catch (UnauthorizedAccessException) { } }

        internal static string QuoteArgument(string value)
        {
            var output = new StringBuilder("\"");
            int slashes = 0;
            foreach (char c in value)
            {
                if (c == '\\') { slashes++; continue; }
                if (c == '"') { output.Append('\\', slashes * 2 + 1); output.Append(c); }
                else { output.Append('\\', slashes); output.Append(c); }
                slashes = 0;
            }
            output.Append('\\', slashes * 2);
            return output.Append('"').ToString();
        }
    }
}
