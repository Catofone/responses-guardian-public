using System;
using System.Collections.Generic;
using System.Globalization;

namespace ResponsesGuardian.Desktop
{
    internal sealed class SettingsDraft
    {
        internal readonly string Listen, Upstream, Provider, Config, Requests, Streams;
        internal SettingsDraft(string listen,string upstream,string provider,string config,string requests,string streams)
        { Listen=listen; Upstream=upstream; Provider=provider; Config=config; Requests=requests; Streams=streams; }
        internal static SettingsDraft From(Dictionary<string,object> values)
        {
            return new SettingsDraft(GuardianRuntime.Text(values,"listen"),GuardianRuntime.Text(values,"upstream"),
                GuardianRuntime.Text(values,"provider"),GuardianRuntime.Text(values,"codex_config"),
                GuardianRuntime.Number(values,"request_max_retries").ToString(CultureInfo.InvariantCulture),
                GuardianRuntime.Number(values,"stream_max_retries").ToString(CultureInfo.InvariantCulture));
        }
        internal static bool ParseRetries(string text,out int value)
        { return int.TryParse(text.Trim(),NumberStyles.None,CultureInfo.InvariantCulture,out value)&&value>=0&&value<=100; }
        internal bool Valid { get { int a,b; return ParseRetries(Requests,out a)&&ParseRetries(Streams,out b); } }
        internal bool SameText(SettingsDraft other)
        {
            return other!=null&&Listen==other.Listen&&Upstream==other.Upstream&&Provider==other.Provider
                &&Config==other.Config&&Requests==other.Requests&&Streams==other.Streams;
        }
        internal bool SameValues(SettingsDraft other)
        {
            if(other==null) return false;
            int a,b,c,d;
            return Listen.Trim()==other.Listen.Trim()&&Upstream.Trim()==other.Upstream.Trim()
                &&Provider.Trim()==other.Provider.Trim()&&Config.Trim()==other.Config.Trim()
                &&ParseRetries(Requests,out a)&&ParseRetries(other.Requests,out b)&&a==b
                &&ParseRetries(Streams,out c)&&ParseRetries(other.Streams,out d)&&c==d;
        }
        internal Dictionary<string,object> ToSettings()
        {
            int a,b;
            if(!ParseRetries(Requests,out a)||!ParseRetries(Streams,out b))
                throw new InvalidOperationException("重试次数须为 0–100 的整数。");
            return new Dictionary<string,object> { {"listen",Listen.Trim()},{"upstream",Upstream.Trim()},
                {"provider",Provider.Trim()},{"codex_config",Config.Trim()},{"request_max_retries",a},{"stream_max_retries",b} };
        }
    }

    internal sealed class SettingsHistory
    {
        private readonly List<SettingsDraft> undo=new List<SettingsDraft>();
        private readonly List<SettingsDraft> redo=new List<SettingsDraft>();
        internal SettingsDraft Current { get; private set; }
        internal SettingsDraft Saved { get; private set; }
        internal bool Dirty { get { return Current!=null&&!Current.SameValues(Saved); } }
        internal bool CanUndo { get { return undo.Count>0; } }
        internal bool CanRedo { get { return redo.Count>0; } }
        internal void Reset(SettingsDraft saved)
        { Current=Saved=saved; undo.Clear(); redo.Clear(); }
        internal void Record(SettingsDraft next)
        {
            if(Current==null) { Reset(next); return; }
            if(Current.SameText(next)) return;
            Push(undo,Current); redo.Clear(); Current=next;
        }
        internal void Undo()
        { if(CanUndo) { Push(redo,Current); Current=Pop(undo); } }
        internal void Redo()
        { if(CanRedo) { Push(undo,Current); Current=Pop(redo); } }
        internal void RefreshSaved(SettingsDraft saved)
        {
            // Identical polls preserve the edit history, even after returning to the saved value.
            if(!Dirty&&!saved.SameValues(Saved)) Reset(saved);
            else Saved=saved;
        }
        internal void MarkSaved(SettingsDraft saved,SettingsDraft submitted)
        {
            // A successful save must not erase edits made while its request was in flight.
            if(Current.SameText(submitted)) Current=saved;
            Saved=saved;
        }
        private static void Push(List<SettingsDraft> list,SettingsDraft value)
        { list.Add(value); if(list.Count>100) list.RemoveAt(0); }
        private static SettingsDraft Pop(List<SettingsDraft> list)
        { var value=list[list.Count-1]; list.RemoveAt(list.Count-1); return value; }
    }
}
