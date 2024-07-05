FROM alpine

RUN apk --no-cache add \
    nodejs \
    npm \
    curl \
    openjdk11 \
    clojure

WORKDIR /app
COPY . /app
RUN npm install --global serve@14.0.1
RUN npm i && npm run release:hoop-ui

EXPOSE 8280

CMD serve -s resources/public -p 8280
