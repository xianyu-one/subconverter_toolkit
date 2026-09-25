FROM ghcr.io/metacubex/subconverter:latest

COPY subconverter_server_conf/include /base/include
COPY subconverter_server_conf/all_base.tpl /base/base/all_base.tpl
COPY subconverter_server_conf/emoji.toml /base/snippets/emoji.toml
COPY subconverter_server_conf/pref.toml /base/pref.toml
COPY all-online.ini /base/config/all-online.ini
COPY lite-online.ini /base/config/lite-online.ini
COPY new.ini /base/config/new.ini
