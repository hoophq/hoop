(ns webapp.connections.helpers
  "Helper functions for working with connections in the webapp.
  Provides utilities for handling connection names, configurations, and data transformations."
  (:require
   ["unique-names-generator" :as ung] ; Library for generating unique names
   [clojure.set :as set]
   [clojure.string :as s]
   [webapp.connections.constants :as constants]))

(defn array->select-options
  "Converts an array of values into a format suitable for select options.

   Takes an array of values and returns a vector of maps with :value and :label keys.
   The label is lowercase with underscores replaced by spaces.

   Example:
   (array->select-options [\"FOO_BAR\"])
   ;=> [{\"value\" \"FOO_BAR\" \"label\" \"foo bar\"}]"
  [array]
  (mapv #(into {} {"value" % "label" (s/lower-case (s/replace % #"_" " "))}) array))

(defn js-select-options->list
  "Converts JavaScript select options into a list of values.

   Takes an array of objects with 'value' keys and returns a vector of just the values.

   Example:
   (js-select-options->list [{\"value\" \"foo\"} {\"value\" \"bar\"}])
   ;=> [\"foo\" \"bar\"]"
  [options]
  (mapv #(get % "value") options))

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

(defn normalize-key
  "Converts a key into a normalized keyword format.

   Takes a key and returns it as a lowercase keyword.

   Example:
   (normalize-key :FOO_BAR) ;=> :foo_bar"
  [k]
  (keyword (clojure.string/lower-case (name k))))

(defn merge-by-key
  "Merges two arrays of maps based on their :key values.

   Preserves required flags and placeholders from arr1 while merging with values from arr2.
   Used to merge connection configuration templates with actual values."
  [arr1 arr2]
  (let [map1 (into {} (map (fn [x] [(normalize-key (:key x)) x]) arr1))
        map2 (into {} (map (fn [x] [(normalize-key (:key x)) x]) arr2))
        all-keys (set/union (set (keys map1)) (set (keys map2)))]
    (mapv
     (fn [k]
       (let [val1 (:value (get map1 k))
             val2 (:value (get map2 k))
             required (:required (get map1 k))
             placeholder (:placeholder (get map1 k))
             hidden (:hidden (get map1 k))
             selected (cond
                        (and (not (empty? val1)) (empty? val2)) (get map1 k)
                        (and (empty? val1) (not (empty? val2))) (assoc (get map2 k)
                                                                       :required required
                                                                       :hidden hidden
                                                                       :placeholder placeholder)
                        :else (if (nil? val1) (assoc (get map2 k)
                                                     :required required
                                                     :hidden hidden
                                                     :placeholder placeholder) (get map1 k)))]
         selected))
     all-keys)))

(defn get-config-keys
  "Gets the required configuration keys for a given connection type.

   Takes a key and returns the corresponding configuration from constants/connection-configs-required."
  [key]
  (get constants/connection-configs-required key))

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

(defn json->config
  "Converts a JSON configuration back into the internal config format.

   Takes a map of config key/values and returns a vector of maps with :key and :value keys.

   Example:
   (json->config {\"FOO\" \"bar\"})
   ;=> [{:key \"FOO\" :value \"bar\"}]"
  [configs]
  (if (or (s/blank? configs) (nil? configs))
    {}
    (->> configs
         (mapv (fn [[key value]] {:key (name key) :value value})))))

(defn separate-values-from-config-by-prefix
  "Separates configuration values by a given prefix (envvar: or filesystem:).

   Takes a config map and prefix string. Returns a new map with the prefixes stripped
   and values base64 decoded."
  [configs prefix]
  (let [regex (if (= prefix "envvar")
                #"envvar:"
                #"filesystem:")]
    (->> configs
         (filter (fn [[k]]
                   (s/includes? (name k) prefix)))
         (map (fn [[k v]]
                {(keyword (s/replace (name k) regex "")) (js/atob v)}))
         (reduce into {}))))


(defn valid-first-char? [value]
  (boolean (re-matches #"[A-Za-z]" value)))

(defn valid-posix? [value]
  (boolean (re-matches #"[A-Za-z][A-Za-z0-9_]*" value)))

(defn parse->posix-format [e input-state]
  (let [new-value (-> e .-target .-value)
        upper-value (s/upper-case new-value)]
    (cond
      (empty? new-value)
      (reset! input-state "")

      (empty? @input-state)
      (when (valid-first-char? new-value)
        (reset! input-state upper-value))

      (valid-posix? new-value)
      (reset! input-state upper-value))))

(defn parse->posix-format-callback [e input-state callback]
  (let [new-value (-> e .-target .-value)
        upper-value (s/upper-case new-value)]
    (cond
      (empty? new-value)
      (callback "")

      (empty? input-state)
      (when (valid-first-char? new-value)
        (callback upper-value))

      (valid-posix? new-value)
      (callback upper-value))))
