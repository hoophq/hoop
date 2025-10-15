(ns webapp.connections.helpers
  "Helper functions for working with connections in the webapp.
  Provides utilities for handling connection names, configurations, and data transformations."
  (:require
   ["unique-names-generator" :as ung] ; Library for generating unique names
   [clojure.string :as s]))

(defn array->select-options
  "Converts an array of values into a format suitable for select options.

   Takes an array of values and returns a vector of maps with :value and :label keys.
   The label is lowercase with underscores replaced by spaces.

   Example:
   (array->select-options [\"FOO_BAR\"])
   ;=> [{\"value\" \"FOO_BAR\" \"label\" \"foo bar\"}]"
  [array]
  (mapv #(into {} {"value" % "label" (s/lower-case (s/replace % #"_" " "))}) array))

(defn random-connection-name
  "Generates a random connection name using animal names and Star Wars references.

   Returns a string in the format \"<name>-<4 digits>\"
   Example: \"wookie-1234\""
  []
  (let [numberDictionary (.generate ung/NumberDictionary #js{:length 4})
        characterName (ung/uniqueNamesGenerator #js{:dictionaries #js[ung/animals ung/starWars]
                                                    :style "lowerCase"
                                                    :length 1})]
    (str characterName "-" numberDictionary)))


(defn config->json
  "Converts configuration maps to a JSON format with prefixed keys.

   Takes a vector of config maps with :key and :value and a prefix string.
   Returns a map with prefixed keys and base64 encoded values.

   For filesystem:SSH_PRIVATE_KEY specifically, adds a newline character at the end
   before base64 encoding, as SSH private keys require a trailing newline.

   Example:
   (config->json [{:key \"foo\" :value \"bar\"}] \"envvar:\")
   ;=> {\"envvar:FOO\" \"<base64 of bar>\"}"
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

(def testable-connection-types
  "Connection types and subtypes that support testing"
  #{"database/postgres"
    "database/mysql"
    "database/mssql"
    "database/mongodb"
    "database/oracledb"
    "custom/dynamodb"
    "custom/cloudwatch"})

(defn can-test-connection?
  "Check if a connection can be tested based on type and subtype"
  [connection]
  (when connection
    (let [connection-type (:type connection)
          connection-subtype (:subtype connection)
          type-subtype-key (str connection-type "/" connection-subtype)]
      (contains? testable-connection-types type-subtype-key))))

(defn is-connection-testing?
  "Check if a connection is currently being tested"
  [test-connection-state connection-name]
  (and test-connection-state
       (:loading test-connection-state)
       (= (:connection-name test-connection-state) connection-name)))

(defn can-connect? [connection]
  (not (and (= "disabled" (:access_mode_runbooks connection))
            (= "disabled" (:access_mode_exec connection))
            (= "disabled" (:access_mode_connect connection)))))

(defn can-open-web-terminal? [connection]
  (if-not (#{"tcp" "httpproxy" "ssh"} (:subtype connection))

    (if (or (= "enabled" (:access_mode_runbooks connection))
            (= "enabled" (:access_mode_exec connection)))
      true
      false)

    false))

(defn can-access-native-client? [connection]
  (and (= "enabled" (:access_mode_connect connection))
      (#{"postgres" "ssh"} (:subtype connection))))
