(ns webapp.resources.helpers
  "Helper functions for working with resources in the webapp."
  (:require
   ["unique-names-generator" :as ung]
   [clojure.string :as s]))

(defn is-onboarding-context?
  "Check if we're currently in onboarding context by URL"
  []
  (s/includes? (.. js/window -location -pathname) "/onboarding"))

(defn random-role-name
  "Generates a random role name based on the parameters passed in.
   Example: \"postgres-readonly-1234\""
  [name]
  (let [numberDictionary (.generate ung/NumberDictionary #js{:length 4})]
    (str name "-role-" numberDictionary)))

(defn config->json
  "Converts configuration maps to a JSON format with prefixed keys.
   Takes a vector of config maps with :key and :value and a prefix string.
   Returns a map with prefixed keys and base64 encoded values."
  [configs prefix]
  (->> configs
       (filter (fn [{:keys [key value]}]
                 (not (or (s/blank? key) (s/blank? value)))))
       (map (fn [{:keys [key value]}]
              (let [prefixed-key (str prefix (s/upper-case key))
                    final-value (if (= prefixed-key "filesystem:SSH_PRIVATE_KEY")
                                  (str value "\n")
                                  value)]
                {prefixed-key (js/btoa final-value)})))
       (reduce into {})))

(defn can-open-web-terminal?
  "Check if a role/connection can open web terminal based on subtype and access modes"
  [role]
  (if-not (#{"tcp" "httpproxy" "ssh" "rdp"} (:subtype role))
    (if (or (= "enabled" (:access_mode_runbooks role))
            (= "enabled" (:access_mode_exec role)))
      true
      false)
    false))

(defn can-access-native-client?
  "Check if a role/connection can access native client based on subtype and access mode"
  [role]
  (and (= "enabled" (:access_mode_connect role))
       (or (= (:subtype role) "postgres")
           (= (:subtype role) "rdp"))))
