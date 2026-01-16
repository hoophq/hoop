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
  (if-not (or (#{"tcp" "ssh" "rdp"} (:subtype role))
              (http-proxy-subtypes (:subtype role)))
    (if (or (= "enabled" (:access_mode_runbooks role))
            (= "enabled" (:access_mode_exec role)))
      true
      false)
    false))

(def ^:private direct-native-subtypes
  #{"postgres" "ssh"})

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
