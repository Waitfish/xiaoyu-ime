cmake_minimum_required(VERSION 3.16)
project(tomato-ime CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(Fcitx5Core REQUIRED)
find_package(PkgConfig REQUIRED)
pkg_check_modules(RIME REQUIRED rime)

add_library(tomato-ime MODULE src/tomato_engine.cpp)
target_include_directories(tomato-ime PRIVATE ${RIME_INCLUDE_DIRS})
target_link_libraries(tomato-ime PRIVATE ${RIME_LIBRARIES} Fcitx5::Core)
target_compile_options(tomato-ime PRIVATE -Wall -Wextra)

# fcitx5 addon 安装目录：CMake 配置变量 → pkg-config → 多架构 lib 目录兜底
include(GNUInstallDirs)
if(NOT Fcitx5_INSTALL_ADDON_DIR)
  pkg_get_variable(Fcitx5_INSTALL_ADDON_DIR Fcitx5Core addonsdir OPTIONAL)
endif()
if(NOT Fcitx5_INSTALL_ADDON_DIR)
  set(Fcitx5_INSTALL_ADDON_DIR "${CMAKE_INSTALL_FULL_LIBDIR}/fcitx5")
endif()
message(STATUS "addon install dir: ${Fcitx5_INSTALL_ADDON_DIR}")
install(TARGETS tomato-ime LIBRARY DESTINATION ${Fcitx5_INSTALL_ADDON_DIR})
