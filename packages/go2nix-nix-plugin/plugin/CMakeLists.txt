cmake_minimum_required(VERSION 3.16)
project(go2nix-nix-plugin)

find_package(PkgConfig REQUIRED)
pkg_check_modules(NIX REQUIRED nix-expr>=2.34 nix-store>=2.34)

find_package(nlohmann_json REQUIRED)

add_library(go2nix_plugin MODULE resolveGoPackages.cc)
target_include_directories(go2nix_plugin PUBLIC ${NIX_INCLUDE_DIRS})
target_compile_options(go2nix_plugin PUBLIC ${NIX_CFLAGS_OTHER})

# The plugin compares this with the evaluator's version when it loads.
target_compile_definitions(go2nix_plugin PRIVATE
  GO2NIX_BUILT_NIX_VERSION="${NIX_nix-expr_VERSION}")

target_link_libraries(go2nix_plugin nlohmann_json::nlohmann_json)

# Link the staticlib explicitly. The Rust crate emits both staticlib and
# cdylib; an unqualified `-lgo2nix_nix_plugin` would let the linker pick
# the .so, which isn't on the Nix plugin's runtime loader path.
target_link_libraries(go2nix_plugin ${RUST_LIB_DIR}/libgo2nix_nix_plugin.a)

if(APPLE)
  set_target_properties(go2nix_plugin PROPERTIES
    LINK_FLAGS "-flat_namespace -undefined suppress")
endif()

install(TARGETS go2nix_plugin DESTINATION lib/nix/plugins)
