cmake_minimum_required(VERSION 3.16)
project(go2nix-nix-plugin)

find_package(PkgConfig REQUIRED)
pkg_check_modules(NIX REQUIRED nix-expr>=2.32 nix-store>=2.32)

find_package(nlohmann_json REQUIRED)

# Nix >=2.34 renamed PrimOp::fun to PrimOp::impl.
include(CheckCXXSourceCompiles)
set(CMAKE_REQUIRED_INCLUDES ${NIX_INCLUDE_DIRS})
set(CMAKE_REQUIRED_FLAGS "${NIX_CFLAGS_OTHER}")
check_cxx_source_compiles("
  #include <nix/expr/eval.hh>
  void check() { auto p = &nix::PrimOp::impl; (void)p; }
  int main() { return 0; }
" NIX_PRIMOP_HAS_IMPL)

add_library(go2nix_plugin MODULE resolveGoPackages.cc)
target_include_directories(go2nix_plugin PUBLIC ${NIX_INCLUDE_DIRS})
target_compile_options(go2nix_plugin PUBLIC ${NIX_CFLAGS_OTHER})

if(NIX_PRIMOP_HAS_IMPL)
  target_compile_definitions(go2nix_plugin PRIVATE NIX_PRIMOP_HAS_IMPL)
endif()
target_link_libraries(go2nix_plugin nlohmann_json::nlohmann_json)

target_link_directories(go2nix_plugin PRIVATE ${RUST_LIB_DIR})
target_link_libraries(go2nix_plugin go2nix_nix_plugin)

if(APPLE)
  set_target_properties(go2nix_plugin PROPERTIES
    LINK_FLAGS "-flat_namespace -undefined suppress")
endif()

install(TARGETS go2nix_plugin DESTINATION lib/nix/plugins)
