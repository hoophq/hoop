(ns webapp.config
  (:require ["./version.js" :as version]
            [webapp.env :as env]))

(def debug?
  ^boolean goog.DEBUG)

(def app-version version)
(println :release env/release-type app-version)

(def release-type env/release-type)
(def api env/api-url)

(def webapp-url env/webapp-url)

(def hoop-app-url env/hoop-app-url)

; TODO remove all these values from and use dotenv (or smt similar)
(def launch-darkly-client-id "614b7c6f3576ea0d34af1b0c")

(def segment-write-key env/segment-write-key)
(def canny-id env/canny-id)

(def sentry-sample-rate env/sentry-sample-rate)
(def sentry-dsn env/sentry-dsn)
