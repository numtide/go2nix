// C++ shim calling snappy's C++ API. Non-empty CXXFiles is what makes
// nix/dag's per-subPackage closure walk set cxx=true (so linkbinary picks
// $CXX for -extld); the snappy::Compress symbols additionally force a
// real libsnappy.so link, not just libstdc++.
#include <snappy.h>
#include <cstdlib>
#include <cstring>
#include <string>

extern "C" int snap_roundtrip(const char* in, size_t in_len, char** out, size_t* out_len) {
    std::string compressed;
    snappy::Compress(in, in_len, &compressed);
    std::string decompressed;
    if (!snappy::Uncompress(compressed.data(), compressed.size(), &decompressed)) {
        return 1;
    }
    *out_len = decompressed.size();
    *out = static_cast<char*>(std::malloc(*out_len));
    std::memcpy(*out, decompressed.data(), *out_len);
    return 0;
}
