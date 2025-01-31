(ns webapp.connections.views.setup.state
  (:require [reagent.core :as r]))

;; Connection type definitions matching UI requirements
(def connection-types
  [{:id "database"
    :title "Database"
    :description "Connect to databases like MySQL, PostgreSQL and more."
    :icon "database"}
   {:id "server"
    :title "Linux VM or Container"
    :description "Set up access to your Linux servers, Docker and Kubernetes."
    :icon "server"}
   {:id "network"
    :title "Network service access (ZTNA)"
    :description "Securely connect to internal network resources."
    :icon "network"}])

(def application-types
  [{:id "ruby-on-rails" :title "Ruby on Rails"}
   {:id "python" :title "Python"}
   {:id "nodejs" :title "Node.js"}
   {:id "clojure" :title "Clojure"}])

(def operation-systems
  [{:id "macos" :title "MacOS"}
   {:id "linux" :title "Linux"}])
