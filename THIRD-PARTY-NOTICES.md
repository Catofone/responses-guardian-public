# Third-party notices

Responses Guardian's own source, desktop manager and original icon are covered
by the project [MIT license](LICENSE). Third-party components retain their
own licenses:

- Go runtime and standard library: BSD-style license, retained in
  [Go-LICENSE.txt](licenses/Go-LICENSE.txt).
- `github.com/klauspost/compress` v1.20.0: its original license file is retained
  in [klauspost-compress-LICENSE.txt](licenses/klauspost-compress-LICENSE.txt).
  Guardian uses the zstd decoder and its dependencies. The original license
  also includes terms for other packages in that module.
- The Snappy reference code used by the decoder: license retained in
  [snappy-reference-LICENSE.txt](licenses/snappy-reference-LICENSE.txt).
- xxhash used by the decoder: MIT license, retained in
  [xxhash-LICENSE.txt](licenses/xxhash-LICENSE.txt).

The desktop manager uses Windows and .NET Framework APIs installed on the
user's computer. Microsoft runtime binaries are not bundled or relicensed
with this project. The desktop build uses no additional GUI library.

The default companion is drawn by the project's own code and is covered
by the MIT license. Optional personal skins are loaded from the user's
local application data directory; they are not embedded in the program
or distributed with the public source and release packages. Their rights
remain with their respective holders. See [asset notes](desktop/assets/ASSETS.md).
