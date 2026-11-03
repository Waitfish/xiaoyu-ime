cmake_minimum_required(VERSION 3.16)
project(tomato-ime CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(Fcitx5Core REQUIRED)
find_package(PkgConfig REQUIRED) # pkg_get_variable 用于 addon 安装目录探测

# M2：模块不再链接 librime（引擎在 Go 守护进程），只需 nlohmann-json（header-only）
find_path(NLOHMANN_JSON_INCLUDE nlohmann/json.hpp REQUIRED)

add_library(tomato-ime MODULE src/tomato_engine.cpp)
target_include_directories(tomato-ime PRIVATE ${NLOHMANN_JSON_INCLUDE})
target_link_libraries(tomato-ime PRIVATE Fcitx5::Core)
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

# addon 元数据与 IM 条目（fcitx5 从 <datadir>/fcitx5/{addon,inputmethod}/ 读取）
if(NOT FCITX_INSTALL_PKGDATADIR)
  set(FCITX_INSTALL_PKGDATADIR "${CMAKE_INSTALL_FULL_DATADIR}/fcitx5")
endif()
install(FILES data/tomato.conf DESTINATION ${FCITX_INSTALL_PKGDATADIR}/addon)
install(FILES data/inputmethod.tomato.conf DESTINATION ${FCITX_INSTALL_PKGDATADIR}/inputmethod
        RENAME tomato.conf)
install(FILES data/tomato-input-method.svg
        DESTINATION ${CMAKE_INSTALL_FULL_DATADIR}/icons/hicolor/scalable/status)
