(ns webapp.resources.helpers
  "Helper functions for working with resources in the webapp."
  (:require
   ["unique-names-generator" :as ung]
   [clojure.string :as s]
   [webapp.resources.constants :refer [http-proxy-subtypes]]))

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

(defn array->select-options
  "Converts an array of values into a format suitable for select options.

   Takes an array of values and returns a vector of maps with :value and :label keys.
   The label is lowercase with underscores replaced by spaces.

   Example:
   (array->select-options [\"FOO_BAR\"])
   ;=> [{\"value\" \"FOO_BAR\" \"label\" \"foo bar\"}]"
  [array]
  (mapv #(into {} {"value" % "label" (s/lower-case (s/replace % #"_" " "))}) array))

(defn config->json
  "Converts configuration maps to a JSON format with prefixed keys.
   Takes a vector of config maps with :key and :value and a prefix string.
   Returns a map with prefixed keys and base64 encoded values."
  [configs prefix & [subtype]]
  (let [is-http-proxy? (http-proxy-subtypes subtype)]
    (->> configs
         (filter (fn [{:keys [key value]}]
                   (not (or (s/blank? key) (s/blank? value)))))
         (map (fn [{:keys [key value]}]
                (let [final-key (if (and is-http-proxy? (s/starts-with? key "HEADER_"))
                                  key
                                  (s/upper-case key))
                      prefixed-key (str prefix final-key)
                      final-value (if (= prefixed-key "filesystem:SSH_PRIVATE_KEY")
                                    (str value "\n")
                                    value)]
                  {prefixed-key (js/btoa final-value)})))
         (reduce into {}))))

(defn can-open-web-terminal?
  "Check if a role/connection can open web terminal based on subtype and access modes"
  [role]
  (if-not (or (#{"tcp" "ssh" "rdp" "github" "git"} (:subtype role))
              (http-proxy-subtypes (:subtype role)))
    (if (or (= "enabled" (:access_mode_runbooks role))
            (= "enabled" (:access_mode_exec role)))
      true
      false)
    false))

(def ^:private direct-native-subtypes
  #{"postgres" "ssh" "github" "git"})

(def ^:private custom-native-subtypes
  #{"rdp" "aws-ssm"})

(defn- native-subtype? [{:keys [subtype type]}]
  (or (direct-native-subtypes subtype)
      (http-proxy-subtypes subtype)
      (and (= type "custom")
           (custom-native-subtypes subtype))))

(defn can-access-native-client?
  "Check if a role/connection can access native client based on subtype and access mode"
  [{:keys [access_mode_connect] :as role}]
  (and (= "enabled" access_mode_connect)
       (native-subtype? role)))

(defn get-secret-prefix
  "Returns the prefix string for a given secret source or provider."
  [source-or-provider]
  (cond
    (= source-or-provider "vault-kv1") "_vaultkv1:"
    (= source-or-provider "vault-kv2") "_vaultkv2:"
    (= source-or-provider "aws-secrets-manager") "_aws:"
    (= source-or-provider "aws-iam-role") "_aws_iam_rds:"
    :else ""))

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

(defn can-hoop-cli? [connection]
  (and (= "enabled" (:access_mode_connect connection))
       (not (and (= (:type connection) "custom")
                 (= (:subtype connection) "rdp")))))
