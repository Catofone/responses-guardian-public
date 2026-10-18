using System;
using System.Drawing;
using System.IO;

namespace ResponsesGuardian.Desktop
{
    internal static class PersonalSkin
    {
        internal static string ImagePath
        {
            get { return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"ResponsesGuardian","skins","companion.png"); }
        }
        internal static Image Load(bool enabled)
        { return enabled?LoadFile(ImagePath):null; }
        internal static Image LoadFile(string path)
        {
            try {
                var file=new FileInfo(path);
                if(!file.Exists||file.Length>8*1024*1024) return null;
                using(var stream=File.OpenRead(path))
                using(var source=Image.FromStream(stream,false,true)) {
                    if(source.Width>4096||source.Height>4096) return null;
                    return new Bitmap(source);
                }
            }
            catch(IOException) { return null; }
            catch(UnauthorizedAccessException) { return null; }
            catch(ArgumentException) { return null; }
            catch(OutOfMemoryException) { return null; } // GDI+ also uses this for invalid image data.
        }
    }
}
