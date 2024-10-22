(ns webapp.components.forms
  (:require ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["@radix-ui/themes" :refer [TextField TextArea Select]]
            [reagent.core :as r]
            [clojure.string :as cs]))

(defn- form-label
  [text]
  [:label {:class "block text-xs font-semibold text-gray-800"} text])


(defn- form-label-dark
  [text]
  [:label {:class "text-white block text-xs font-semibold"} text])

(defn- form-helper-text
  [text dark]
  [:div {:class (str "relative flex flex-col group"
                     (when dark " text-white"))}
   [:> hero-outline-icon/QuestionMarkCircleIcon {:class "w-4 h-4"
                                                 :aria-hidden "true"}]
   [:div {:class "absolute bottom-0 flex-col hidden mb-6 w-max group-hover:flex"}
    [:span {:class (str "relative border -left-3 border-gray-300 bg-white rounded-md z-50 "
                        "p-2 text-xs text-gray-700 leading-none whitespace-no-wrap shadow-lg")}
     text]
    [:div {:class "w-3 h-3 -mt-2 border-r border-b border-gray-300 bg-white transform rotate-45"}]]])

(defn input
  "Multi purpose HTML input component.
  Props signature:
  :label -> html label text;
  :placeholder -> html prop placeholder for input;
  :value -> a reagent atom piece of state."
  [_]
  (fn
    [{:keys [type on-blur on-focus]}]
    (let [local-type (r/atom (or type "text"))
          on-blur-cb (fn []
                       (when (= type "password")
                         (reset! local-type "password"))
                       (when on-blur (on-blur)))
          on-focus-cb (fn []
                        (when (= type "password")
                          (reset! local-type "text"))
                        (when on-focus (on-focus)))]
      (fn [{:keys [label
                   placeholder
                   name
                   size
                   dark
                   id
                   helper-text
                   value
                   defaultValue
                   on-change
                   on-keyDown
                   required
                   full-width?
                   pattern
                   disabled
                   minlength
                   maxlength
                   min
                   max
                   step
                   not-margin-bottom? ;; TODO: Remove this prop when remove margin-bottom from all inputs
                   hidden]}]
        [:div {:class (str "text-sm"
                           (when-not not-margin-bottom? " mb-regular")
                           (when full-width? " w-full")
                           (when hidden " hidden"))}
         [:div {:class "flex items-center gap-2 mb-1"}
          (when label
            (if dark
              [form-label-dark label]
              [form-label label]))
          (when (not (cs/blank? helper-text))
            [form-helper-text helper-text dark])]
         [:> TextField.Root
          (merge
           {:type (if (= "string" @local-type) "text" @local-type)
            :id id
            :size (or size "3")
            :class (when dark "dark")
            :placeholder (or placeholder label)
            :name name
            :pattern pattern
            :minLength minlength
            :maxLength maxlength
            :min min
            :max max
            :step step
            :value value
            :on-change on-change
            :on-keyDown on-keyDown
            :on-blur on-blur-cb
            :on-focus on-focus-cb
            :disabled (or disabled false)
            :required (or required false)}
           (when defaultValue
             {:defaultValue defaultValue}))]]))))

(defn input-metadata [{:keys [label name id placeholder disabled required value on-change]}]
  [:div {:class "relative"}
   [:label {:htmlFor label
            :class "absolute -top-2 left-2 inline-block bg-white px-1 text-xs font-medium text-gray-900"}
    label]
   [:> TextField.Root {:type "text"
                       :size "1"
                       :name name
                       :id id
                       :class "dark"
                       :placeholder placeholder
                       :disabled (or disabled false)
                       :required (or required false)
                       :on-change on-change
                       :value value}]])

(defn textarea
  [{:keys [label
           placeholder
           name
           dark
           id
           helper-text
           value
           defaultValue
           on-change
           on-keyDown
           required
           on-blur
           rows
           autoFocus
           not-margin-bottom? ;; TODO: Remove this prop when remove margin-bottom from all inputs
           disabled]}]
  [:div {:class (when-not not-margin-bottom? "mb-regular")}
   [:div {:class "flex items-center gap-2 mb-1"}
    (if dark
      [form-label-dark label]
      [form-label label])
    (when (not (cs/blank? helper-text))
      [form-helper-text helper-text dark])]
   [:> TextArea
    (merge
     {:class (when dark "dark")
      :id (or id "")
      :rows (or rows 5)
      :name (or name "")
      :value value
      :autoFocus autoFocus
      :placeholder placeholder
      :on-change on-change
      :on-blur on-blur
      :on-keyDown on-keyDown
      :disabled (or disabled false)
      :required (or required false)}
     (when defaultValue
       {:defaultValue defaultValue}))]])

(defn- option
  [item _]
  ^{:key (:value item)}
  [:> Select.Item {:value (:value item)} (:text item)])

(defn select
  "HTML select.
  Props signature:
  label -> html label text;
  options -> List of {:text string :value string};
  active -> the option value of an already active item;
  on-change -> function to be executed on change;
  required -> HTML required attribute;"
  [{:keys [label helper-text name size options placeholder selected default-value on-change required disabled full-width? dark]}]
  [:div {:class "mb-regular text-sm w-full"}
   [:div {:class "flex items-center gap-2 mb-1"}
    (if dark
      [form-label-dark label]
      [form-label label])
    (when (not (cs/blank? helper-text))
      [form-helper-text helper-text dark])]
   [:> Select.Root {:size (or size "3")
                    :name name
                    :value selected
                    :default-value default-value
                    :on-value-change on-change
                    :required (or required false)
                    :disabled (or disabled false)}
    [:> Select.Trigger {:placeholder (or placeholder "Select one")
                        :class (str (when full-width? "w-full ")
                                    (when dark "dark"))}]
    [:> Select.Content {:position "popper"}
     (map #(option % selected) options)]]])

(defn select-editor
  "HTML select.
  Props signature:
  label -> html label text;
  options -> List of {:text string :value string};
  active -> the option value of an already active item;
  on-change -> function to be executed on change;
  required -> HTML required attribute;"
  [{:keys [name size options selected on-change full-width? required disabled]}]
  [:div {:class "text-xs"}
   [:> Select.Root {:size (or size "1")
                    :name name
                    :value selected
                    :on-value-change on-change
                    :required (or required false)
                    :disabled (or disabled false)}
    [:> Select.Trigger {:class (str "dark"
                                    (when full-width? "w-full"))
                        :placeholder "Select one"}]
    [:> Select.Content {:position "popper"}
     (map #(option % selected) options)]]])
