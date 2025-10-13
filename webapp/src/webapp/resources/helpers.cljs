(ns webapp.resources.helpers
  "Helper functions for working with resources in the webapp."
  (:require
   ["unique-names-generator" :as ung]
   [clojure.string :as s]))

(defn random-resource-name
  "Generates a random resource name.
   Returns a string in the format \"<name>-<4 digits>\"
   Example: \"database-1234\""
  []
  (let [numberDictionary (.generate ung/NumberDictionary #js{:length 4})
        characterName (ung/uniqueNamesGenerator #js{:dictionaries #js[ung/animals ung/starWars]
                                                    :style "lowerCase"
                                                    :length 1})]
    (str characterName "-" numberDictionary)))

(defn random-role-name
  "Generates a random role name based on resource type.
   Example: \"postgres-readonly-1234\""
  [resource-type]
  (let [numberDictionary (.generate ung/NumberDictionary #js{:length 4})]
    (str resource-type "-role-" numberDictionary)))

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

(defn is-special-type?
  "Check if a resource type requires special handling"
  [type subtype]
  (contains? #{"postgres" "mysql" "mongodb" "mssql" "oracledb" "ssh" "tcp" "httpproxy"}
             subtype))

(defn get-resource-category
  "Get the category of a resource based on type"
  [type]
  (case type
    "database" "database"
    "application" "application"
    "custom" "custom"
    "custom"))
