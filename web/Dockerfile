# Longwave Frontend.
#
# Nur statische Dateien — kein Build-Schritt, keine Abhaengigkeiten, kein
# Bundler. Das Frontend baut seine Server-URL zur Laufzeit aus dem eigenen
# Origin, deshalb gibt es hier auch keine Backend-Adresse einzubacken: dasselbe
# Image laeuft lokal, im kind-Cluster und hinter jedem Ingress (12 Factor X).

FROM nginx:1.29-alpine

ARG VERSION=dev
LABEL org.opencontainers.image.title="longwave-web" \
      org.opencontainers.image.description="Frontend für Longwave Online" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.source="https://github.com/toto-b/longlongwave"

# Die mitgelieferte Standardkonfiguration weicht der eigenen.
RUN rm -f /etc/nginx/conf.d/default.conf
COPY nginx.conf /etc/nginx/nginx.conf

COPY index.html style.css app.js /usr/share/nginx/html/

# Die Version fuer schnelle Sichtprüfung ablegen: `curl /version.txt` zeigt,
# welcher Release gerade ausgeliefert wird.
RUN printf '%s\n' "${VERSION}" > /usr/share/nginx/html/version.txt

# nginx laeuft unprivilegiert. Die Verzeichnisse, in die es schreibt, muessen
# dem nginx-Benutzer gehoeren, damit readOnlyRootFilesystem im Deployment
# zusammen mit einem tmpfs auf /tmp und /var/cache/nginx funktioniert.
RUN chown -R nginx:nginx /usr/share/nginx/html /var/cache/nginx \
    && chmod -R a+rX /usr/share/nginx/html

USER nginx

EXPOSE 8080

# Konfiguration einmal beim Bauen pruefen, damit ein Tippfehler nicht erst im
# Cluster als CrashLoopBackOff auffaellt.
RUN nginx -t -c /etc/nginx/nginx.conf

ENTRYPOINT ["nginx", "-g", "daemon off;"]
