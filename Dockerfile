FROM scratch

ADD shorten /shorten

ENTRYPOINT [ "/shorten" ]
