// std::string forces a real libstdc++ dependency, so linking with CC
// instead of CXX as -extld fails (undefined std::__cxx11::* symbols) —
// that link failure is the regression check for transitive CXX detection.
#include <string>
#include <cstring>
#include <cstdlib>

extern "C" char* cxx_concat(const char* a, const char* b) {
    std::string s(a);
    s += b;
    char* out = static_cast<char*>(std::malloc(s.size() + 1));
    std::memcpy(out, s.c_str(), s.size() + 1);
    return out;
}
